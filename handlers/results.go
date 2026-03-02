package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"pdfEkstrak/database"

	"github.com/gofiber/fiber/v2"
)

type ResultsHandler struct {
	db *database.DB
}

func NewResultsHandler(db *database.DB) *ResultsHandler {
	return &ResultsHandler{db: db}
}

// GET /api/results/:jobID/status
func (h *ResultsHandler) GetStatus(c *fiber.Ctx) error {
	jobID := c.Params("jobID")

	job, err := h.db.GetJobByID(jobID)
	if err != nil {
		log.Printf("[RESULTS] Job not found: %s (%v)", jobID, err)
		return c.Status(http.StatusNotFound).JSON(fiber.Map{
			"success": false,
			"error":   "Job not found",
		})
	}

	return c.Status(http.StatusOK).JSON(fiber.Map{
		"success":      true,
		"job_id":       job.ID,
		"file_name":    job.FileName,
		"status":       job.Status,
		"error_msg":    job.ErrorMsg,
		"created_at":   job.CreatedAt,
		"updated_at":   job.UpdatedAt,
		"completed_at": job.CompletedAt,
	})
}

// GET /api/results/:jobID
func (h *ResultsHandler) GetJob(c *fiber.Ctx) error {
	jobID := c.Params("jobID")
	log.Printf("[RESULTS] Fetching results for job %s", jobID)

	// Verify job exists
	job, err := h.db.GetJobByID(jobID)
	if err != nil {
		log.Printf("[RESULTS] Job not found: %s (%v)", jobID, err)
		return c.Status(http.StatusNotFound).JSON(fiber.Map{
			"success": false,
			"error":   "Job not found",
		})
	}

	// Get extracted data
	rows, err := h.db.GetExtractedDataByJobID(job.ID)
	if err != nil {
		log.Printf("[RESULTS] DB error: %v", err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"success": false,
			"error":   "Database error",
		})
	}

	if len(rows) == 0 {
		log.Printf("[RESULTS] No data for job %s", jobID)
		return c.Status(http.StatusOK).JSON(fiber.Map{
			"success": false,
			"error":   "No data extracted",
			"items":   []fiber.Map{},
			"total":   0,
		})
	}

	// Process items - parse and group
	var allText strings.Builder // Combine semua text
	textCombined := false
	tableMap := make(map[string]fiber.Map)
	items := make([]fiber.Map, 0)

	for idx, r := range rows {
		if r.DataType == "text" {
			// Append semua text
			if textCombined {
				allText.WriteString("\n\n") // Separator antar text
			}
			allText.WriteString(r.Content)
			textCombined = true

		} else if r.DataType == "table" {
			// Parse table
			tableContent := parseTableContent(r.Content)

			if tableContent == nil {
				log.Printf("[RESULTS] Failed to parse table at index %d", idx)
				continue
			}

			headers := tableContent["headers"].([]interface{})
			headerKey := headersToKey(headers)

			if existing, ok := tableMap[headerKey]; ok {
				existingRows := existing["rows"].([]interface{})
				newRows := tableContent["rows"].([]interface{})
				existing["rows"] = append(existingRows, newRows...)
				existing["row_count"] = len(existing["rows"].([]interface{}))
				tableMap[headerKey] = existing
			} else {
				tableMap[headerKey] = fiber.Map{
					"id":         r.ID,
					"type":       "table",
					"headers":    headers,
					"rows":       tableContent["rows"],
					"page":       r.PageNumber,
					"created_at": r.CreatedAt,
				}
			}
		}
	}

	// Add combined text as first item
	if textCombined {
		items = append(items, fiber.Map{
			"id":         "text-combined",
			"type":       "text",
			"content":    allText.String(),
			"confidence": 1.0,
		})
	}

	// Add merged tables
	for _, table := range tableMap {
		items = append(items, table)
	}

	textCount := 1
	if !textCombined {
		textCount = 0
	}
	tableCount := len(tableMap)

	log.Printf("[RESULTS] Returning %d items (%d text, %d merged tables) for job %s", len(items), textCount, tableCount, jobID)
	return c.Status(http.StatusOK).JSON(fiber.Map{
		"success":     true,
		"job_id":      job.ID,
		"text_count":  textCount,
		"table_count": tableCount,
		"items":       items,
		"total":       len(items),
	})
}

// Helper: Parse table content - simplified
func parseTableContent(content string) map[string]interface{} {
	if content == "" {
		return nil
	}

	var tableJSON map[string]interface{}

	// Try direct unmarshal first (content should be proper JSON now)
	err := json.Unmarshal([]byte(content), &tableJSON)
	if err != nil {
		log.Printf("[DEBUG] Parse error: %v", err)
		log.Printf("[DEBUG] Content preview: %s", content[:min(1000, len(content))])
		return nil
	}

	// Validate required fields
	if _, hasHeaders := tableJSON["headers"]; !hasHeaders {
		log.Printf("[DEBUG] No 'headers' field in table")
		return nil
	}

	if _, hasRows := tableJSON["rows"]; !hasRows {
		log.Printf("[DEBUG] No 'rows' field in table")
		return nil
	}

	return tableJSON
}

// Helper: Unescape JSON string
func unescapeJSON(s string) string {
	// Handle multiple levels of escaping
	result := s

	// Unescape quotes
	result = strings.ReplaceAll(result, "\\\"", "\"")
	result = strings.ReplaceAll(result, "\\'", "'")

	// Unescape newlines
	result = strings.ReplaceAll(result, "\\n", "\n")
	result = strings.ReplaceAll(result, "\\r", "\r")
	result = strings.ReplaceAll(result, "\\t", "\t")

	// Unescape backslashes (do last to avoid over-unescaping)
	result = strings.ReplaceAll(result, "\\\\", "\\")

	return result
}

// Helper: Convert headers to key for grouping
func headersToKey(headers []interface{}) string {
	if len(headers) == 0 {
		return "empty"
	}

	keys := make([]string, len(headers))
	for i, h := range headers {
		keys[i] = fmt.Sprintf("%v", h)
	}
	return strings.Join(keys, "|")
}

// Helper: Min function
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// GET /api/results (list jobs)
func (h *ResultsHandler) ListJobs(c *fiber.Ctx) error {
	limit := 10
	offset := 0

	jobs, err := h.db.GetAllJobs(limit, offset)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"success": false,
			"error":   "Database error",
		})
	}

	jobsList := make([]fiber.Map, 0, len(jobs))
	for _, job := range jobs {
		jobsList = append(jobsList, fiber.Map{
			"job_id":     job.ID,
			"file_name":  job.FileName,
			"status":     job.Status,
			"created_at": job.CreatedAt,
		})
	}

	return c.Status(http.StatusOK).JSON(fiber.Map{
		"success": true,
		"jobs":    jobsList,
		"total":   len(jobsList),
	})
}

// GET /api/health
func (h *ResultsHandler) Health(c *fiber.Ctx) error {
	return c.Status(http.StatusOK).JSON(fiber.Map{
		"status":  "ok",
		"service": "pdf-extractor",
	})
}
