# Complete Go Code - Copy & Paste All Files

## File 1: `main.go`

```go
package main

import (
	"log"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"pdf-extractor/config"
	"pdf-extractor/database"
	"pdf-extractor/handlers"
	"pdf-extractor/services"
)

func main() {
	cfg := config.LoadConfig()
	log.Printf("🚀 Starting PDF Extractor Server")
	log.Printf("   Port: %s | Environment: %s", cfg.ServerPort, cfg.Environment)

	db, err := database.Initialize(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("❌ Database initialization failed: %v", err)
	}
	defer db.Close()

	extractionSvc := services.NewExtractionService(db, cfg)

	app := fiber.New(fiber.Config{
		AppName:   "PDF Extractor API",
		BodyLimit: int(cfg.MaxFileSize),
	})

	app.Use(logger.New())
	app.Use(recover.New())
	app.Static("/", "./templates")

	uploadHandler := handlers.NewUploadHandler(db, cfg, extractionSvc)
	resultsHandler := handlers.NewResultsHandler(db)

	api := app.Group("/api")
	api.Post("/upload", uploadHandler.Handle)
	api.Get("/results", resultsHandler.ListJobs)
	api.Get("/results/:jobID", resultsHandler.GetJob)
	api.Get("/results/:jobID/status", resultsHandler.GetStatus)
	api.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok", "service": "pdf-extractor"})
	})

	log.Printf("✅ Server running on :%s", cfg.ServerPort)
	if err := app.Listen(":" + cfg.ServerPort); err != nil {
		log.Fatalf("❌ Server error: %v", err)
	}
}
```

## File 2: `config/config.go`

```go
package config

import (
	"os"
	"github.com/joho/godotenv"
)

type AppConfig struct {
	ServerPort       string
	Environment      string
	DatabasePath     string
	MaxFileSize      int64
	UploadDir        string
	PythonScriptPath string
	PythonExecutable string
}

func LoadConfig() *AppConfig {
	_ = godotenv.Load()

	return &AppConfig{
		ServerPort:       getEnv("PORT", "3000"),
		Environment:      getEnv("ENV", "development"),
		DatabasePath:     getEnv("DATABASE_PATH", "./database.db"),
		MaxFileSize:      50 * 1024 * 1024,
		UploadDir:        getEnv("UPLOAD_DIR", "./uploads"),
		PythonScriptPath: getEnv("PYTHON_SCRIPT", "./python/extract.py"),
		PythonExecutable: getEnv("PYTHON_EXEC", "python3"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
```

## File 3: `database/models.go`

```go
package database

import "time"

type ExtractionJob struct {
	ID          string     `db:"id"`
	FileName    string     `db:"file_name"`
	FileSize    int64      `db:"file_size"`
	Status      string     `db:"status"`
	ErrorMsg    string     `db:"error_msg"`
	CreatedAt   time.Time  `db:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at"`
	CompletedAt *time.Time `db:"completed_at"`
}

type ExtractedData struct {
	ID         string    `db:"id"`
	JobID      string    `db:"job_id"`
	DataType   string    `db:"data_type"`
	Content    string    `db:"content"`
	PageNumber int       `db:"page_number"`
	Confidence float64   `db:"confidence"`
	CreatedAt  time.Time `db:"created_at"`
}

type ExtractionResult struct {
	JobID       string      `json:"job_id"`
	FileName    string      `json:"file_name"`
	Status      string      `json:"status"`
	DataItems   []DataItem  `json:"data_items"`
	ProcessedAt string      `json:"processed_at,omitempty"`
	ErrorMsg    string      `json:"error_msg,omitempty"`
}

type DataItem struct {
	Type       string      `json:"type"`
	Content    interface{} `json:"content"`
	PageNumber int         `json:"page_number"`
	Confidence float64     `json:"confidence,omitempty"`
}

type TableData struct {
	Headers  []string   `json:"headers"`
	Rows     [][]string `json:"rows"`
	RowCount int        `json:"row_count"`
}
```

## File 4: `database/db.go`

```go
package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"github.com/mattn/go-sqlite3"
	"github.com/google/uuid"
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

func (db *DB) GetJob(jobID string) (*ExtractionJob, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	job := &ExtractionJob{}
	query := `SELECT id, file_name, file_size, status, error_msg, created_at, updated_at, completed_at FROM extraction_jobs WHERE id = ?`

	err := db.conn.QueryRow(query, jobID).Scan(
		&job.ID, &job.FileName, &job.FileSize, &job.Status,
		&job.ErrorMsg, &job.CreatedAt, &job.UpdatedAt, &job.CompletedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("job not found")
	}
	if err != nil {
		return nil, fmt.Errorf("query error: %w", err)
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

func (db *DB) SaveExtractedData(jobID string, dataType string, content string, pageNum int) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	dataID := uuid.New().String()
	query := `INSERT INTO extracted_data (id, job_id, data_type, content, page_number) VALUES (?, ?, ?, ?, ?)`

	_, err := db.conn.Exec(query, dataID, jobID, dataType, content, pageNum)
	return err
}

func (db *DB) GetJobResults(jobID string) (*ExtractionResult, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	job := &ExtractionJob{}
	jobQuery := `SELECT id, file_name, status, error_msg, completed_at FROM extraction_jobs WHERE id = ?`

	err := db.conn.QueryRow(jobQuery, jobID).Scan(
		&job.ID, &job.FileName, &job.Status, &job.ErrorMsg, &job.CompletedAt,
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

	if job.CompletedAt != nil {
		result.ProcessedAt = job.CompletedAt.Format("2006-01-02 15:04:05")
	}

	return result, nil
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

func (db *DB) Close() error {
	return db.conn.Close()
}
```

## File 5: `handlers/upload.go`

```go
package handlers

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"github.com/gofiber/fiber/v2"
	"pdf-extractor/config"
	"pdf-extractor/database"
	"pdf-extractor/services"
)

type UploadHandler struct {
	db            *database.DB
	cfg           *config.AppConfig
	extractionSvc *services.ExtractionService
}

func NewUploadHandler(db *database.DB, cfg *config.AppConfig, extractionSvc *services.ExtractionService) *UploadHandler {
	return &UploadHandler{db: db, cfg: cfg, extractionSvc: extractionSvc}
}

func (h *UploadHandler) Handle(c *fiber.Ctx) error {
	log.Println("📥 Upload request received")

	form, err := c.MultipartForm()
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"error": "Failed to parse form data"})
	}

	files := form.File["files"]
	if len(files) == 0 {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"error": "No files provided"})
	}

	if len(files) > 10 {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"error": "Maximum 10 files allowed"})
	}

	type JobResult struct {
		JobID    string `json:"job_id"`
		FileName string `json:"file_name"`
		Status   string `json:"status"`
	}

	var results []JobResult
	var errors []string

	for _, fileHeader := range files {
		result, err := h.processFile(fileHeader)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", fileHeader.Filename, err))
			continue
		}
		results = append(results, *result)
	}

	return c.Status(http.StatusOK).JSON(fiber.Map{
		"success":        len(results) > 0,
		"jobs":           results,
		"failed":         errors,
		"total_uploaded": len(results),
	})
}

func (h *UploadHandler) processFile(fileHeader *fiber.MultipartFileHeader) (*struct {
	JobID    string
	FileName string
	Status   string
}, error) {
	if err := h.validateFile(fileHeader); err != nil {
		return nil, err
	}

	file, err := fileHeader.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	jobID, err := h.db.CreateJob(fileHeader.Filename, fileHeader.Size)
	if err != nil {
		return nil, fmt.Errorf("failed to create job: %w", err)
	}
	log.Printf("✓ Job created: %s", jobID)

	savedPath, err := h.saveFile(jobID, file, fileHeader.Filename)
	if err != nil {
		h.db.UpdateJobStatus(jobID, "FAILED", err.Error())
		return nil, err
	}
	log.Printf("✓ File saved: %s", savedPath)

	go h.extractionSvc.ExtractFromPDF(jobID, savedPath)

	return &struct {
		JobID    string
		FileName string
		Status   string
	}{JobID: jobID, FileName: fileHeader.Filename, Status: "PENDING"}, nil
}

func (h *UploadHandler) validateFile(fileHeader *fiber.MultipartFileHeader) error {
	if fileHeader.Size > h.cfg.MaxFileSize {
		return fmt.Errorf("file too large")
	}
	ext := filepath.Ext(fileHeader.Filename)
	if ext != ".pdf" {
		return fmt.Errorf("only PDF files allowed")
	}
	return nil
}

func (h *UploadHandler) saveFile(jobID string, file io.Reader, filename string) (string, error) {
	if err := os.MkdirAll(h.cfg.UploadDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	savedFilename := fmt.Sprintf("%s_%s", jobID, filename)
	filePath := filepath.Join(h.cfg.UploadDir, savedFilename)

	dst, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to create file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		return "", fmt.Errorf("failed to save file: %w", err)
	}

	return filePath, nil
}
```

## File 6: `handlers/results.go`

```go
package handlers

import (
	"net/http"
	"strconv"
	"github.com/gofiber/fiber/v2"
	"pdf-extractor/database"
)

type ResultsHandler struct {
	db *database.DB
}

func NewResultsHandler(db *database.DB) *ResultsHandler {
	return &ResultsHandler{db: db}
}

func (h *ResultsHandler) GetJob(c *fiber.Ctx) error {
	jobID := c.Params("jobID")

	result, err := h.db.GetJobResults(jobID)
	if err != nil {
		return c.Status(http.StatusNotFound).JSON(fiber.Map{"error": "Job not found"})
	}

	return c.Status(http.StatusOK).JSON(result)
}

func (h *ResultsHandler) GetStatus(c *fiber.Ctx) error {
	jobID := c.Params("jobID")

	job, err := h.db.GetJob(jobID)
	if err != nil {
		return c.Status(http.StatusNotFound).JSON(fiber.Map{"error": "Job not found"})
	}

	return c.Status(http.StatusOK).JSON(fiber.Map{
		"job_id":      job.ID,
		"file_name":   job.FileName,
		"status":      job.Status,
		"error_msg":   job.ErrorMsg,
		"created_at":  job.CreatedAt.Format("2006-01-02 15:04:05"),
		"completed_at": job.CompletedAt,
	})
}

func (h *ResultsHandler) ListJobs(c *fiber.Ctx) error {
	page, _ := strconv.Atoi(c.Query("page", "1"))
	limit, _ := strconv.Atoi(c.Query("limit", "10"))

	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 10
	}

	offset := (page - 1) * limit

	jobs, err := h.db.GetAllJobs(limit, offset)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to retrieve jobs"})
	}

	return c.Status(http.StatusOK).JSON(fiber.Map{
		"jobs":  jobs,
		"page":  page,
		"limit": limit,
		"count": len(jobs),
	})
}
```

## File 7: `services/extraction_service.go`

```go
package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"pdf-extractor/config"
	"pdf-extractor/database"
)

type ExtractionService struct {
	db  *database.DB
	cfg *config.AppConfig
}

type PythonResponse struct {
	Success bool            `json:"success"`
	Error   string          `json:"error,omitempty"`
	Data    []ExtractedItem `json:"data"`
}

type ExtractedItem struct {
	Type       string      `json:"type"`
	Content    string      `json:"content"`
	PageNumber int         `json:"page_number"`
	Confidence float64     `json:"confidence"`
	Metadata   interface{} `json:"metadata,omitempty"`
}

func NewExtractionService(db *database.DB, cfg *config.AppConfig) *ExtractionService {
	return &ExtractionService{db: db, cfg: cfg}
}

func (s *ExtractionService) ExtractFromPDF(jobID string, filePath string) {
	log.Printf("[Job %s] 🔄 Starting extraction", jobID)

	if err := s.db.UpdateJobStatus(jobID, "PROCESSING", ""); err != nil {
		log.Printf("[Job %s] ❌ Failed to update status: %v", jobID, err)
		return
	}

	pythonOutput, err := s.callPythonExtractor(filePath)
	if err != nil {
		log.Printf("[Job %s] ❌ Python error: %v", jobID, err)
		s.db.UpdateJobStatus(jobID, "FAILED", err.Error())
		return
	}

	response := &PythonResponse{}
	if err := json.Unmarshal([]byte(pythonOutput), response); err != nil {
		log.Printf("[Job %s] ❌ Failed to parse JSON: %v", jobID, err)
		s.db.UpdateJobStatus(jobID, "FAILED", "Invalid JSON response")
		return
	}

	if !response.Success {
		log.Printf("[Job %s] ❌ Python error: %s", jobID, response.Error)
		s.db.UpdateJobStatus(jobID, "FAILED", response.Error)
		return
	}

	for _, item := range response.Data {
		if err := s.db.SaveExtractedData(jobID, item.Type, item.Content, item.PageNumber); err != nil {
			log.Printf("[Job %s] ⚠️ Failed to save data: %v", jobID, err)
		}
	}

	if err := s.db.UpdateJobStatus(jobID, "COMPLETED", ""); err != nil {
		log.Printf("[Job %s] ❌ Failed to mark complete: %v", jobID, err)
		return
	}

	log.Printf("[Job %s] ✅ Extraction completed", jobID)
}

func (s *ExtractionService) callPythonExtractor(filePath string) (string, error) {
	log.Printf("🐍 Executing Python script on %s", filePath)

	cmd := exec.Command(s.cfg.PythonExecutable, s.cfg.PythonScriptPath, filePath)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("python failed: %w (stderr: %s)", err, stderr.String())
	}

	output := stdout.String()
	if output == "" {
		return "", fmt.Errorf("python returned empty output")
	}

	return output, nil
}
```

## File 8: `.env`

```bash
PORT=3000
ENV=development
DATABASE_PATH=./database.db
UPLOAD_DIR=./uploads
PYTHON_SCRIPT=./python/extract.py
PYTHON_EXEC=python3
```

## File 9: `go.mod`

```mod
module pdf-extractor

go 1.21

require (
	github.com/gofiber/fiber/v2 v2.52.0
	github.com/google/uuid v1.6.0
	github.com/joho/godotenv v1.5.1
	github.com/mattn/go-sqlite3 v1.14.18
)
```