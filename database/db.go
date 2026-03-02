package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	conn *sql.DB
	mu   sync.RWMutex
}

func Initialize(dbPath string) (*DB, error) {
	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	db := &DB{conn: conn}

	if err := db.createTables(); err != nil {
		return nil, fmt.Errorf("failed to create tables: %w", err)
	}

	log.Println("✓ Database initialized")
	return db, nil
}

func (db *DB) createTables() error {
	schema := `
	CREATE TABLE IF NOT EXISTS extraction_jobs (
		id TEXT PRIMARY KEY,
		file_name TEXT NOT NULL,
		file_size INTEGER NOT NULL,
		status TEXT NOT NULL DEFAULT 'PENDING',
		error_msg TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		completed_at DATETIME
	);

	CREATE TABLE IF NOT EXISTS extracted_data (
		id TEXT PRIMARY KEY,
		job_id TEXT NOT NULL,
		data_type TEXT NOT NULL,
		content TEXT NOT NULL,
		page_number INTEGER,
		confidence REAL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (job_id) REFERENCES extraction_jobs(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_extraction_jobs_status ON extraction_jobs(status);
	CREATE INDEX IF NOT EXISTS idx_extraction_jobs_created ON extraction_jobs(created_at);
	CREATE INDEX IF NOT EXISTS idx_extracted_data_job_id ON extracted_data(job_id);
	`

	_, err := db.conn.Exec(schema)
	return err
}

// ============== JOB OPERATIONS ==============

func (db *DB) CreateJob(fileName string, fileSize int64) (string, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	jobID := uuid.New().String()
	query := `INSERT INTO extraction_jobs (id, file_name, file_size, status) VALUES (?, ?, ?, 'PENDING')`
	_, err := db.conn.Exec(query, jobID, fileName, fileSize)
	if err != nil {
		return "", fmt.Errorf("failed to create job: %w", err)
	}

	return jobID, nil
}

func (db *DB) GetJobByID(jobID string) (*ExtractionJob, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	job := &ExtractionJob{}
	query := `SELECT id, file_name, file_size, status, error_msg, created_at, updated_at, completed_at FROM extraction_jobs WHERE id = ?`

	var completedAt sql.NullString
	err := db.conn.QueryRow(query, jobID).Scan(
		&job.ID, &job.FileName, &job.FileSize, &job.Status,
		&job.ErrorMsg, &job.CreatedAt, &job.UpdatedAt, &completedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("job not found")
	}
	if err != nil {
		return nil, fmt.Errorf("query error: %w", err)
	}

	// Handle NULL completed_at
	if completedAt.Valid {
		job.CompletedAt = completedAt.String
	} else {
		job.CompletedAt = ""
	}

	return job, nil
}

func (db *DB) UpdateJobStatus(jobID string, status string, errorMsg string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	query := `UPDATE extraction_jobs SET status = ?, error_msg = ?, updated_at = CURRENT_TIMESTAMP`
	if status == "COMPLETED" {
		query += `, completed_at = CURRENT_TIMESTAMP`
	}
	query += ` WHERE id = ?`

	_, err := db.conn.Exec(query, status, errorMsg, jobID)
	return err
}

func (db *DB) GetAllJobs(limit int, offset int) ([]ExtractionJob, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	query := `SELECT id, file_name, file_size, status, created_at, updated_at FROM extraction_jobs ORDER BY created_at DESC LIMIT ? OFFSET ?`
	rows, err := db.conn.Query(query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []ExtractionJob
	for rows.Next() {
		job := ExtractionJob{}
		err := rows.Scan(&job.ID, &job.FileName, &job.FileSize, &job.Status, &job.CreatedAt, &job.UpdatedAt)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}

	return jobs, nil
}

// ============== EXTRACTED DATA OPERATIONS ==============

func (db *DB) SaveExtractedData(jobID string, dataType string, content string, pageNum int) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	dataID := uuid.New().String()
	query := `INSERT INTO extracted_data (id, job_id, data_type, content, page_number, confidence) VALUES (?, ?, ?, ?, ?, ?)`

	_, err := db.conn.Exec(query, dataID, jobID, dataType, content, pageNum, 1.0)
	return err
}

func (db *DB) GetExtractedDataByJobID(jobID string) ([]ExtractedDataRecord, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	query := `
		SELECT id, job_id, data_type, content, page_number, COALESCE(confidence, 1.0), created_at
		FROM extracted_data
		WHERE job_id = ?
		ORDER BY page_number ASC, created_at ASC
	`

	rows, err := db.conn.Query(query, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []ExtractedDataRecord
	for rows.Next() {
		var record ExtractedDataRecord
		err := rows.Scan(
			&record.ID,
			&record.JobID,
			&record.DataType,
			&record.Content,
			&record.PageNumber,
			&record.Confidence,
			&record.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		results = append(results, record)
	}

	return results, rows.Err()
}

// ============== STRUCTS ==============

type ExtractionJob struct {
	ID          string
	FileName    string
	FileSize    int64
	Status      string
	ErrorMsg    string
	CreatedAt   string
	UpdatedAt   string
	CompletedAt string
}

type ExtractedDataRecord struct {
	ID         string
	JobID      string
	DataType   string
	Content    string
	PageNumber int
	Confidence float64
	CreatedAt  string
}

type ExtractionResult struct {
	JobID       string
	FileName    string
	Status      string
	DataItems   []DataItem
	ErrorMsg    string
	ProcessedAt string
}

type DataItem struct {
	Type       string
	Content    interface{}
	PageNumber int
	Confidence float64
}

type TableData struct {
	Headers  []string   `json:"headers"`
	Rows     [][]string `json:"rows"`
	RowCount int        `json:"row_count"`
}

func (db *DB) GetJobResults(jobID string) (*ExtractionResult, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	job := &ExtractionJob{}
	jobQuery := `SELECT id, file_name, status, error_msg, completed_at FROM extraction_jobs WHERE id = ?`

	var completedAt sql.NullTime
	err := db.conn.QueryRow(jobQuery, jobID).Scan(
		&job.ID, &job.FileName, &job.Status, &job.ErrorMsg, &completedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("job not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get job: %w", err)
	}

	dataQuery := `SELECT data_type, content, page_number, confidence FROM extracted_data WHERE job_id = ? ORDER BY page_number, rowid`
	rows, err := db.conn.Query(dataQuery, jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get extracted data: %w", err)
	}
	defer rows.Close()

	var dataItems []DataItem
	for rows.Next() {
		var dataType string
		var content string
		var pageNum int
		var confidence float64

		if err := rows.Scan(&dataType, &content, &pageNum, &confidence); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		var contentObj interface{}
		if dataType == "table" {
			var table TableData
			if err := json.Unmarshal([]byte(content), &table); err != nil {
				contentObj = content
			} else {
				contentObj = table
			}
		} else {
			contentObj = content
		}

		dataItems = append(dataItems, DataItem{
			Type:       dataType,
			Content:    contentObj,
			PageNumber: pageNum,
			Confidence: confidence,
		})
	}

	result := &ExtractionResult{
		JobID:     job.ID,
		FileName:  job.FileName,
		Status:    job.Status,
		DataItems: dataItems,
		ErrorMsg:  job.ErrorMsg,
	}

	if completedAt.Valid {
		result.ProcessedAt = completedAt.Time.Format("2006-01-02 15:04:05")
	}

	return result, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}
