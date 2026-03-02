package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"pdfEkstrak/config"
	"pdfEkstrak/database"
)

// ExtractionService bertanggung jawab untuk ekstraksi PDF
// Ini adalah service layer yang menangani business logic
type ExtractionService struct {
	db  *database.DB
	cfg *config.AppConfig
}

// ExtractionResult adalah struktur output dari Python script
type ExtractionResult struct {
	Success bool       `json:"success"`
	Error   string     `json:"error"`
	Data    []DataItem `json:"data"`
}

// DataItem adalah unit ekstraksi individual (text atau table)
type DataItem struct {
	Type       string                 `json:"type"`    // "text" atau "table"
	Content    interface{}            `json:"content"` // Isi ekstraksi
	PageNumber int                    `json:"page_number"`
	Confidence float64                `json:"confidence"` // Confidence score 0-1
	Metadata   map[string]interface{} `json:"metadata"`   // Additional metadata
}

// NewExtractionService membuat instance baru ExtractionService
func NewExtractionService(db *database.DB, cfg *config.AppConfig) *ExtractionService {
	return &ExtractionService{
		db:  db,
		cfg: cfg,
	}
}

// ExtractFromPDF melakukan ekstraksi PDF dan save hasil ke database
// Method ini di-call sebagai background goroutine dari upload handler
// IMPORTANT: Non-blocking operation
func (s *ExtractionService) ExtractFromPDF(jobID string, filePath string) {
	log.Printf("[Job %s] 🚀 [Extraction] Starting extraction process", jobID)

	// Update job status to PROCESSING
	if err := s.db.UpdateJobStatus(jobID, "PROCESSING", ""); err != nil {
		log.Printf("[Job %s] ❌ Failed to update status: %v", jobID, err)
		return
	}

	// Run Python extraction script
	result, err := s.runPythonScript(filePath)
	if err != nil {
		log.Printf("[Job %s] ❌ [Python] Execution error: %v", jobID, err)
		s.db.UpdateJobStatus(jobID, "FAILED", fmt.Sprintf("Extraction error: %v", err))
		return
	}

	// Check if extraction was successful
	if !result.Success {
		log.Printf("[Job %s] ❌ [Extraction] Failed: %s", jobID, result.Error)
		s.db.UpdateJobStatus(jobID, "FAILED", result.Error)
		return
	}

	log.Printf("[Job %s] 📊 [Processing] Got %d items to save", jobID, len(result.Data))

	// Save extracted data ke database
	// IMPORTANT: Save setiap item secara individual
	for idx, item := range result.Data {
		var contentStr string

		// Prepare content berdasarkan tipe
		if item.Type == "table" {
			// Untuk table, pastikan dalam format JSON string
			if contentBytes, isBytes := item.Content.([]byte); isBytes {
				contentStr = string(contentBytes)
			} else if strContent, isStr := item.Content.(string); isStr {
				contentStr = strContent
			} else {
				// Marshal object ke JSON string
				contentBytes, _ := json.Marshal(item.Content)
				contentStr = string(contentBytes)
			}
			log.Printf("[Job %s]    [Item %d] Table saved (%d bytes)",
				jobID, idx+1, len(contentStr))
		} else {
			// Untuk text, convert ke string
			contentStr = fmt.Sprintf("%v", item.Content)
			log.Printf("[Job %s]    [Item %d] Text saved (%d bytes)",
				jobID, idx+1, len(contentStr))
		}

		// Save ke database
		if err := s.db.SaveExtractedData(
			jobID,
			item.Type,
			contentStr,
			item.PageNumber,
		); err != nil {
			log.Printf("[Job %s] ❌ Failed to save item %d: %v", jobID, idx, err)
			s.db.UpdateJobStatus(jobID, "FAILED",
				fmt.Sprintf("Failed to save item %d", idx))
			return
		}
	}

	// Update job status to COMPLETED
	log.Printf("[Job %s] ✅ [Extraction] Completed successfully", jobID)
	s.db.UpdateJobStatus(jobID, "COMPLETED", "")
}

// runPythonScript menjalankan Python script untuk ekstraksi PDF
// IMPORTANT: Ini memanggil external Python process
func (s *ExtractionService) runPythonScript(filePath string) (*ExtractionResult, error) {
	scriptPath := s.cfg.PythonScript

	// Verify script exists
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("python script not found: %s", scriptPath)
	}

	// Verify PDF file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("pdf file not found: %s", filePath)
	}

	// Get absolute paths (normalize untuk cross-platform)
	absScriptPath, _ := filepath.Abs(scriptPath)
	absFilePath, _ := filepath.Abs(filePath)

	// Convert backslash to forward slash untuk Python (Windows compatibility)
	pythonScriptPath := strings.ReplaceAll(absScriptPath, "\\", "/")
	pythonFilePath := strings.ReplaceAll(absFilePath, "\\", "/")

	log.Printf("[Python] Executing script:")
	log.Printf("[Python]   Script: %s", pythonScriptPath)
	log.Printf("[Python]   File: %s", pythonFilePath)

	// Create command untuk execute Python
	cmd := exec.Command("python", pythonScriptPath, pythonFilePath)

	// Capture stdout dan stderr
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Execute dengan timeout (max 30 seconds)
	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	select {
	case err := <-done:
		// Process completed
		if err != nil {
			log.Printf("[Python] ❌ Exit error: %v", err)
			log.Printf("[Python] stderr: %s", stderr.String())
			log.Printf("[Python] stdout: %s", stdout.String())
			return nil, fmt.Errorf("python execution failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		// Timeout
		cmd.Process.Kill()
		return nil, fmt.Errorf("python script timeout (>30s)")
	}

	// Parse JSON output dari Python
	var result ExtractionResult
	stdoutStr := stdout.String()

	log.Printf("[Python] Output length: %d bytes", len(stdoutStr))

	if err := json.Unmarshal([]byte(stdoutStr), &result); err != nil {
		log.Printf("[Python] ❌ JSON parse error: %v", err)
		log.Printf("[Python] stdout: %s", stdoutStr)
		log.Printf("[Python] stderr: %s", stderr.String())
		return nil, fmt.Errorf("failed to parse python output: %v", err)
	}

	log.Printf("[Python] ✓ Parsed %d items successfully", len(result.Data))
	return &result, nil
}
