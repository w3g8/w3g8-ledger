package sepa

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"

	"finplatform/internal/domain"
	"finplatform/internal/events"
)

// ReportStatus represents the processing status of a report.
type ReportStatus string

const (
	ReportPending    ReportStatus = "PENDING"
	ReportProcessing ReportStatus = "PROCESSING"
	ReportProcessed  ReportStatus = "PROCESSED"
	ReportFailed     ReportStatus = "FAILED"
)

// Report represents a SEPA status report.
type Report struct {
	ID              string
	ReportType      string // pain.002, camt.053, camt.054
	FilePath        string
	FileHash        string
	Status          ReportStatus
	PaymentsUpdated int
	ErrorMessage    string
	ReceivedAt      time.Time
	ProcessedAt     *time.Time
}

// StatusUpdate represents a payment status update from a report.
type StatusUpdate struct {
	MsgID            string
	PmtInfID         string
	EndToEndID       string
	Status           SEPAStatus
	RejectReasonCode string
	RejectReasonDesc string
	BookingDate      *time.Time
}

// ReportStore handles report persistence.
type ReportStore struct {
	pool *pgxpool.Pool
}

// NewReportStore creates a new report store.
func NewReportStore(pool *pgxpool.Pool) *ReportStore {
	return &ReportStore{pool: pool}
}

// Create inserts a new report record.
func (s *ReportStore) Create(ctx context.Context, report *Report) error {
	query := `
		INSERT INTO sepa_reports (id, report_type, file_path, file_hash, status, payments_updated, error_message, received_at, processed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (file_hash) DO NOTHING
	`

	result, err := s.pool.Exec(ctx, query,
		report.ID,
		report.ReportType,
		report.FilePath,
		report.FileHash,
		report.Status,
		report.PaymentsUpdated,
		nullableString(report.ErrorMessage),
		report.ReceivedAt,
		report.ProcessedAt,
	)
	if err != nil {
		return fmt.Errorf("insert report: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("report already processed: %s", report.FileHash)
	}

	return nil
}

// MarkProcessed marks a report as processed.
func (s *ReportStore) MarkProcessed(ctx context.Context, id string, paymentsUpdated int) error {
	now := time.Now()
	query := `UPDATE sepa_reports SET status = $2, payments_updated = $3, processed_at = $4 WHERE id = $1`
	_, err := s.pool.Exec(ctx, query, id, ReportProcessed, paymentsUpdated, now)
	return err
}

// MarkFailed marks a report as failed.
func (s *ReportStore) MarkFailed(ctx context.Context, id string, errorMsg string) error {
	query := `UPDATE sepa_reports SET status = $2, error_message = $3 WHERE id = $1`
	_, err := s.pool.Exec(ctx, query, id, ReportFailed, errorMsg)
	return err
}

// EventPublisher publishes events.
type EventPublisher interface {
	Publish(ctx context.Context, subject string, env *events.Envelope) error
}

// ReportIngester processes SEPA status reports.
type ReportIngester struct {
	paymentStore *PostgresStore
	reportStore  *ReportStore
	publisher    EventPublisher
	logger       *slog.Logger
}

// NewReportIngester creates a new report ingester.
func NewReportIngester(paymentStore *PostgresStore, reportStore *ReportStore, publisher EventPublisher, logger *slog.Logger) *ReportIngester {
	return &ReportIngester{
		paymentStore: paymentStore,
		reportStore:  reportStore,
		publisher:    publisher,
		logger:       logger,
	}
}

// IngestFile processes a SEPA report file.
func (i *ReportIngester) IngestFile(ctx context.Context, filePath string) error {
	// Read file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	// Calculate hash for idempotency
	hash := sha256.Sum256(data)
	fileHash := hex.EncodeToString(hash[:])

	// Detect report type from content
	reportType := i.detectReportType(data)

	// Create report record
	report := &Report{
		ID:         ulid.Make().String(),
		ReportType: reportType,
		FilePath:   filePath,
		FileHash:   fileHash,
		Status:     ReportPending,
		ReceivedAt: time.Now(),
	}

	if err := i.reportStore.Create(ctx, report); err != nil {
		return fmt.Errorf("create report record: %w", err)
	}

	i.logger.Info("ingesting SEPA report",
		"report_id", report.ID,
		"type", reportType,
		"file", filePath,
	)

	// Parse based on type
	var updates []StatusUpdate
	switch reportType {
	case "pain.002":
		updates, err = i.ParsePain002(data)
	case "camt.053":
		updates, err = i.ParseCamt053(data)
	default:
		err = fmt.Errorf("unsupported report type: %s", reportType)
	}

	if err != nil {
		i.reportStore.MarkFailed(ctx, report.ID, err.Error())
		return fmt.Errorf("parse report: %w", err)
	}

	// Apply updates
	paymentsUpdated := 0
	for _, update := range updates {
		if err := i.applyUpdate(ctx, report.ID, update); err != nil {
			i.logger.Warn("failed to apply update",
				"msg_id", update.MsgID,
				"pmt_inf_id", update.PmtInfID,
				"error", err,
			)
			continue
		}
		paymentsUpdated++
	}

	// Mark report as processed
	if err := i.reportStore.MarkProcessed(ctx, report.ID, paymentsUpdated); err != nil {
		return fmt.Errorf("mark processed: %w", err)
	}

	i.logger.Info("SEPA report processed",
		"report_id", report.ID,
		"payments_updated", paymentsUpdated,
	)

	return nil
}

func (i *ReportIngester) detectReportType(data []byte) string {
	// Simple detection based on root element
	if containsBytes(data, []byte("<pain.002")) || containsBytes(data, []byte("pain.002.")) {
		return "pain.002"
	}
	if containsBytes(data, []byte("<camt.053")) || containsBytes(data, []byte("camt.053.")) {
		return "camt.053"
	}
	if containsBytes(data, []byte("<camt.054")) || containsBytes(data, []byte("camt.054.")) {
		return "camt.054"
	}
	return "unknown"
}

func (i *ReportIngester) applyUpdate(ctx context.Context, reportID string, update StatusUpdate) error {
	// Update payment status
	err := i.paymentStore.UpdateFromReport(ctx, update.MsgID, update.PmtInfID, reportID,
		update.Status, update.RejectReasonCode, update.RejectReasonDesc)
	if err != nil {
		return err
	}

	// Publish settlement event for terminal statuses
	if update.Status == SEPASettled || update.Status == SEPARejected {
		i.publishSettlement(ctx, update)
	}

	return nil
}

func (i *ReportIngester) publishSettlement(ctx context.Context, update StatusUpdate) {
	if i.publisher == nil {
		return
	}

	status := "SETTLED"
	if update.Status == SEPARejected {
		status = "FAILED"
	}

	settlement := events.ProviderSettlement{
		Provider:    "sepa",
		ProviderRef: fmt.Sprintf("%s:%s", update.MsgID, update.PmtInfID),
		Status:      status,
		ErrorCode:   update.RejectReasonCode,
		ErrorMsg:    update.RejectReasonDesc,
		SettledAt:   time.Now(),
	}

	env, err := events.NewEnvelope("provider.settlement.v1", domain.TenantID(""), update.EndToEndID, &settlement)
	if err != nil {
		i.logger.Error("failed to create settlement envelope", "error", err)
		return
	}

	if err := i.publisher.Publish(ctx, "provider.settlement", env); err != nil {
		i.logger.Error("failed to publish settlement event", "error", err)
	}
}

// Pain002 XML structures (ISO 20022 Payment Status Report)
type Pain002Document struct {
	XMLName xml.Name       `xml:"Document"`
	CstmrPmtStsRpt Pain002Report `xml:"CstmrPmtStsRpt"`
}

type Pain002Report struct {
	GrpHdr     Pain002GrpHdr     `xml:"GrpHdr"`
	OrgnlGrpInfAndSts Pain002OrgnlGrpInfAndSts `xml:"OrgnlGrpInfAndSts"`
	OrgnlPmtInfAndSts []Pain002OrgnlPmtInfAndSts `xml:"OrgnlPmtInfAndSts"`
}

type Pain002GrpHdr struct {
	MsgId   string `xml:"MsgId"`
	CreDtTm string `xml:"CreDtTm"`
}

type Pain002OrgnlGrpInfAndSts struct {
	OrgnlMsgId   string `xml:"OrgnlMsgId"`
	OrgnlMsgNmId string `xml:"OrgnlMsgNmId"`
	GrpSts       string `xml:"GrpSts"`
}

type Pain002OrgnlPmtInfAndSts struct {
	OrgnlPmtInfId string              `xml:"OrgnlPmtInfId"`
	PmtInfSts     string              `xml:"PmtInfSts"`
	TxInfAndSts   []Pain002TxInfAndSts `xml:"TxInfAndSts"`
}

type Pain002TxInfAndSts struct {
	OrgnlEndToEndId string           `xml:"OrgnlEndToEndId"`
	TxSts           string           `xml:"TxSts"`
	StsRsnInf       *Pain002StsRsnInf `xml:"StsRsnInf"`
}

type Pain002StsRsnInf struct {
	Rsn  Pain002Rsn `xml:"Rsn"`
	AddtlInf string `xml:"AddtlInf"`
}

type Pain002Rsn struct {
	Cd string `xml:"Cd"`
}

// ParsePain002 parses a pain.002 Payment Status Report.
func (i *ReportIngester) ParsePain002(data []byte) ([]StatusUpdate, error) {
	var doc Pain002Document
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("unmarshal pain.002: %w", err)
	}

	var updates []StatusUpdate

	orgnlMsgId := doc.CstmrPmtStsRpt.OrgnlGrpInfAndSts.OrgnlMsgId

	for _, pmtInf := range doc.CstmrPmtStsRpt.OrgnlPmtInfAndSts {
		for _, tx := range pmtInf.TxInfAndSts {
			update := StatusUpdate{
				MsgID:      orgnlMsgId,
				PmtInfID:   pmtInf.OrgnlPmtInfId,
				EndToEndID: tx.OrgnlEndToEndId,
				Status:     i.mapPain002Status(tx.TxSts),
			}

			if tx.StsRsnInf != nil {
				update.RejectReasonCode = tx.StsRsnInf.Rsn.Cd
				update.RejectReasonDesc = tx.StsRsnInf.AddtlInf
			}

			updates = append(updates, update)
		}
	}

	return updates, nil
}

func (i *ReportIngester) mapPain002Status(txSts string) SEPAStatus {
	switch txSts {
	case "ACCP", "ACSP", "ACSC": // Accepted
		return SEPAAccepted
	case "ACWC": // Accepted with Change
		return SEPAAccepted
	case "RJCT": // Rejected
		return SEPARejected
	case "PDNG": // Pending
		return SEPASubmitted
	default:
		return SEPASubmitted
	}
}

// Camt053 XML structures (ISO 20022 Bank to Customer Statement)
type Camt053Document struct {
	XMLName xml.Name      `xml:"Document"`
	BkToCstmrStmt Camt053BkToCstmrStmt `xml:"BkToCstmrStmt"`
}

type Camt053BkToCstmrStmt struct {
	GrpHdr Camt053GrpHdr `xml:"GrpHdr"`
	Stmt   []Camt053Stmt  `xml:"Stmt"`
}

type Camt053GrpHdr struct {
	MsgId   string `xml:"MsgId"`
	CreDtTm string `xml:"CreDtTm"`
}

type Camt053Stmt struct {
	Id      string       `xml:"Id"`
	Ntry    []Camt053Ntry `xml:"Ntry"`
}

type Camt053Ntry struct {
	Amt       Camt053Amt   `xml:"Amt"`
	CdtDbtInd string       `xml:"CdtDbtInd"` // CRDT or DBIT
	Sts       string       `xml:"Sts"`       // BOOK, PDNG
	BookgDt   Camt053Dt    `xml:"BookgDt"`
	NtryDtls  []Camt053NtryDtls `xml:"NtryDtls"`
}

type Camt053Amt struct {
	Value string `xml:",chardata"`
	Ccy   string `xml:"Ccy,attr"`
}

type Camt053Dt struct {
	Dt string `xml:"Dt"`
}

type Camt053NtryDtls struct {
	TxDtls []Camt053TxDtls `xml:"TxDtls"`
}

type Camt053TxDtls struct {
	Refs Camt053Refs `xml:"Refs"`
}

type Camt053Refs struct {
	MsgId      string `xml:"MsgId"`
	PmtInfId   string `xml:"PmtInfId"`
	EndToEndId string `xml:"EndToEndId"`
}

// ParseCamt053 parses a camt.053 Bank Statement.
func (i *ReportIngester) ParseCamt053(data []byte) ([]StatusUpdate, error) {
	var doc Camt053Document
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("unmarshal camt.053: %w", err)
	}

	var updates []StatusUpdate

	for _, stmt := range doc.BkToCstmrStmt.Stmt {
		for _, ntry := range stmt.Ntry {
			// Only process booked debit entries (outgoing payments)
			if ntry.Sts != "BOOK" || ntry.CdtDbtInd != "DBIT" {
				continue
			}

			for _, dtls := range ntry.NtryDtls {
				for _, tx := range dtls.TxDtls {
					update := StatusUpdate{
						MsgID:      tx.Refs.MsgId,
						PmtInfID:   tx.Refs.PmtInfId,
						EndToEndID: tx.Refs.EndToEndId,
						Status:     SEPASettled,
					}

					updates = append(updates, update)
				}
			}
		}
	}

	return updates, nil
}

// IngestFromReader processes a report from an io.Reader.
func (i *ReportIngester) IngestFromReader(ctx context.Context, r io.Reader, reportType string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read data: %w", err)
	}

	hash := sha256.Sum256(data)
	fileHash := hex.EncodeToString(hash[:])

	report := &Report{
		ID:         ulid.Make().String(),
		ReportType: reportType,
		FilePath:   "stream",
		FileHash:   fileHash,
		Status:     ReportPending,
		ReceivedAt: time.Now(),
	}

	if err := i.reportStore.Create(ctx, report); err != nil {
		return fmt.Errorf("create report record: %w", err)
	}

	var updates []StatusUpdate
	switch reportType {
	case "pain.002":
		updates, err = i.ParsePain002(data)
	case "camt.053":
		updates, err = i.ParseCamt053(data)
	default:
		err = fmt.Errorf("unsupported report type: %s", reportType)
	}

	if err != nil {
		i.reportStore.MarkFailed(ctx, report.ID, err.Error())
		return fmt.Errorf("parse report: %w", err)
	}

	paymentsUpdated := 0
	for _, update := range updates {
		if err := i.applyUpdate(ctx, report.ID, update); err != nil {
			continue
		}
		paymentsUpdated++
	}

	return i.reportStore.MarkProcessed(ctx, report.ID, paymentsUpdated)
}

func containsBytes(data, substr []byte) bool {
	for i := 0; i <= len(data)-len(substr); i++ {
		if string(data[i:i+len(substr)]) == string(substr) {
			return true
		}
	}
	return false
}
