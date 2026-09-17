package api

import (
	"encoding/json"
	"fmt"
	"goapi/db"
	"goapi/gemini"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/xuri/excelize/v2"
)

var autoExportMu sync.Mutex

// AutoExportAnalytics automatically refreshes the clean latest Excel (.xlsx) and JSON (.json) analytics reports
func AutoExportAnalytics() {
	go func() {
		autoExportMu.Lock()
		defer autoExportMu.Unlock()

		outputDir := "./output"
		_ = os.MkdirAll(outputDir, 0755)

		latestExcelPath := filepath.Join(outputDir, "gemini_analytics.xlsx")
		latestJSONPath := filepath.Join(outputDir, "gemini_analytics.json")

		if _, err := ExportAnalyticsToExcel(latestExcelPath); err != nil {
			log.Printf("⚠️ Auto-export Excel warning: %v", err)
		}
		if _, _, err := ExportAnalyticsToJSON(latestJSONPath); err != nil {
			log.Printf("⚠️ Auto-export JSON warning: %v", err)
		}
		log.Printf("📊 Auto-exported dual analytics: %s & %s", latestExcelPath, latestJSONPath)
	}()
}

// HandleExcelExport handles GET /export/excel
func HandleExcelExport(c fiber.Ctx) error {
	filePath, err := ExportAnalyticsToExcel("")
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("Failed to export excel: %v", err)})
	}
	return c.Download(filePath)
}

// HandleJSONExport handles GET /v1/analytics and GET /export/json
func HandleJSONExport(c fiber.Ctx) error {
	latestJSONPath := filepath.Join("./output", "gemini_analytics.json")
	_, data, err := ExportAnalyticsToJSON(latestJSONPath)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("Failed to export JSON analytics: %v", err)})
	}
	c.Set("Content-Type", "application/json")
	return c.Send(data)
}

// ExportAnalyticsToJSON compiles system stats, history, and financial ROI into structured JSON
func ExportAnalyticsToJSON(outputPath string) (string, []byte, error) {
	if outputPath == "" {
		outputDir := "./output"
		_ = os.MkdirAll(outputDir, 0755)
		outputPath = filepath.Join(outputDir, "gemini_analytics.json")
	}

	messages, _ := db.GetAllMessagesForExport()
	media, _ := db.GetAllMediaForExport()
	accounts, _ := db.GetAccounts()
	stats, _ := db.GetSystemStats()

	totalTokens := 0
	for _, m := range messages {
		if tok, ok := m["tokens"].(int); ok {
			totalTokens += tok
		}
	}

	mediaCounts := map[string]int{"image": 0, "video": 0, "music": 0}
	for _, md := range media {
		if mType, ok := md["type"].(string); ok {
			mediaCounts[strings.ToLower(mType)]++
		}
	}

	report := fiber.Map{
		"generated_at": time.Now().Format(time.RFC3339),
		"engine": fiber.Map{
			"name":          "Free Gemini API",
			"version":       "3.8",
			"primary_model": "Google Gemini 3.8 Flash",
			"bpe_tokenizer": "cl100k_base (tiktoken-go)",
			"database":      "SQLite (WAL Mode)",
			"transport":     "HTTP/3 QUIC + HTTP/2 TLS Fallback",
		},
		"summary": fiber.Map{
			"total_messages":   len(messages),
			"total_tokens":     totalTokens,
			"total_media":      len(media),
			"total_requests":   stats["total_requests"],
			"media_breakdown":  mediaCounts,
			"tracked_accounts": len(accounts),
			"active_workers":   gemini.GetActiveWorkerCount(),
		},
		"financial_roi": fiber.Map{
			"cost_saved_usd":           stats["cost_saved_usd"],
			"cost_saved_usd_formatted": stats["cost_saved_usd_formatted"],
			"cost_saved_inr":           stats["cost_saved_inr"],
			"cost_saved_inr_formatted": stats["cost_saved_inr_formatted"],
			"official_pricing_benchmarks": fiber.Map{
				"gemini_3_8_flash_per_1m_tokens_usd": 0.50,
				"imagen_3_per_image_usd":              0.030,
				"veo_per_video_usd":                   1.20,
				"music_per_track_usd":                 0.08,
				"usd_to_inr_exchange_rate":            db.GetUSDToINRRate(),
			},
		},
		"recent_messages":   messages,
		"media_generations": media,
		"accounts":          accounts,
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", nil, err
	}

	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		return "", nil, err
	}

	return outputPath, data, nil
}

// ExportAnalyticsToExcel creates a multi-sheet formatted Excel workbook
func ExportAnalyticsToExcel(outputPath string) (string, error) {
	if outputPath == "" {
		outputDir := "./output"
		_ = os.MkdirAll(outputDir, 0755)
		outputPath = filepath.Join(outputDir, "gemini_analytics.xlsx")
	}

	messages, _ := db.GetAllMessagesForExport()
	media, _ := db.GetAllMediaForExport()
	accounts, _ := db.GetAccounts()
	stats, _ := db.GetSystemStats()

	f := excelize.NewFile()
	defer f.Close()

	// ─── Sheet 1: 📋 Chat History ───────────────────────────────────────────
	sheetMessages := "📋 Chat History"
	f.SetSheetName("Sheet1", sheetMessages)
	msgHeaders := []string{"ID", "Conv ID", "User / IP", "Role", "Model", "Est. Tokens", "Created At", "Message Content"}
	var msgRows [][]interface{}
	totalTokens := 0
	for _, m := range messages {
		tok, _ := m["tokens"].(int)
		totalTokens += tok
		msgRows = append(msgRows, []interface{}{
			m["id"],
			m["conversation_id"],
			m["user_id"],
			m["role"],
			m["model"],
			tok,
			m["created_at"],
			m["content"],
		})
	}
	_ = renderStyledSheet(f, sheetMessages, msgRows, msgHeaders, "#1E293B", "#1877F2")

	// ─── Sheet 2: 🎨 Generated Media ────────────────────────────────────────
	sheetMedia := "🎨 Generated Media"
	f.NewSheet(sheetMedia)
	mediaHeaders := []string{"ID", "Media Type", "Prompt", "File Name", "Download URL", "Aspect Ratio", "Response ID", "Created At"}
	var mediaRows [][]interface{}
	mediaCounts := map[string]int{"image": 0, "video": 0, "music": 0}
	for _, md := range media {
		mType, _ := md["type"].(string)
		mediaCounts[strings.ToLower(mType)]++
		mediaRows = append(mediaRows, []interface{}{
			md["id"],
			md["type"],
			md["prompt"],
			md["file_name"],
			md["url"],
			md["aspect_ratio"],
			md["response_id"],
			md["created_at"],
		})
	}
	_ = renderStyledSheet(f, sheetMedia, mediaRows, mediaHeaders, "#581C87", "#9333EA")

	// ─── Sheet 3: 📊 Executive Analytics ────────────────────────────────────
	sheetAnalytics := "📊 Executive Analytics"
	f.NewSheet(sheetAnalytics)
	_ = renderAnalyticsSummarySheet(f, sheetAnalytics, len(messages), len(media), totalTokens, stats, mediaCounts, accounts)

	// Set Sheet 1 as active view
	activeIdx, _ := f.GetSheetIndex(sheetMessages)
	f.SetActiveSheet(activeIdx)

	if err := f.SaveAs(outputPath); err != nil {
		return "", err
	}

	return outputPath, nil
}

func renderStyledSheet(f *excelize.File, sheetName string, rows [][]interface{}, headers []string, headerBgColor string, tabColorRGB string) error {
	_ = f.SetSheetProps(sheetName, &excelize.SheetPropsOptions{
		TabColorRGB: &tabColorRGB,
	})

	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Family: "Arial", Size: 11, Bold: true, Color: "#FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{headerBgColor}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "#CBD5E1", Style: 1},
			{Type: "right", Color: "#CBD5E1", Style: 1},
			{Type: "top", Color: "#CBD5E1", Style: 1},
			{Type: "bottom", Color: "#CBD5E1", Style: 1},
		},
	})

	cellStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Family: "Arial", Size: 10},
		Alignment: &excelize.Alignment{Horizontal: "left", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "#E2E8F0", Style: 1},
			{Type: "right", Color: "#E2E8F0", Style: 1},
			{Type: "top", Color: "#E2E8F0", Style: 1},
			{Type: "bottom", Color: "#E2E8F0", Style: 1},
		},
	})

	centerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Family: "Arial", Size: 10},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "#E2E8F0", Style: 1},
			{Type: "right", Color: "#E2E8F0", Style: 1},
			{Type: "top", Color: "#E2E8F0", Style: 1},
			{Type: "bottom", Color: "#E2E8F0", Style: 1},
		},
	})

	wrapStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Family: "Arial", Size: 10},
		Alignment: &excelize.Alignment{Horizontal: "left", Vertical: "top", WrapText: true},
		Border: []excelize.Border{
			{Type: "left", Color: "#E2E8F0", Style: 1},
			{Type: "right", Color: "#E2E8F0", Style: 1},
			{Type: "top", Color: "#E2E8F0", Style: 1},
			{Type: "bottom", Color: "#E2E8F0", Style: 1},
		},
	})

	// Header row
	_ = f.SetRowHeight(sheetName, 1, 28)
	for colIdx, headerText := range headers {
		cell, _ := excelize.CoordinatesToCellName(colIdx+1, 1)
		_ = f.SetCellValue(sheetName, cell, headerText)
		_ = f.SetCellStyle(sheetName, cell, cell, headerStyle)
	}

	// Data rows
	for rowIdx, rowData := range rows {
		rowNum := rowIdx + 2
		_ = f.SetRowHeight(sheetName, rowNum, 22)
		for colIdx, val := range rowData {
			cell, _ := excelize.CoordinatesToCellName(colIdx+1, rowNum)
			_ = f.SetCellValue(sheetName, cell, val)

			header := headers[colIdx]
			if strings.Contains(header, "Content") || strings.Contains(header, "Prompt") {
				_ = f.SetCellStyle(sheetName, cell, cell, wrapStyle)
			} else if strings.Contains(header, "ID") || strings.Contains(header, "Tokens") || strings.Contains(header, "Created") || strings.Contains(header, "Role") || strings.Contains(header, "Type") {
				_ = f.SetCellStyle(sheetName, cell, cell, centerStyle)
			} else {
				_ = f.SetCellStyle(sheetName, cell, cell, cellStyle)
			}
		}
	}

	// Auto-fit column widths
	for colIdx, header := range headers {
		colLetter, _ := excelize.ColumnNumberToName(colIdx + 1)
		if strings.Contains(header, "Content") || strings.Contains(header, "Prompt") {
			_ = f.SetColWidth(sheetName, colLetter, colLetter, 50)
		} else if strings.Contains(header, "URL") {
			_ = f.SetColWidth(sheetName, colLetter, colLetter, 35)
		} else if strings.Contains(header, "Conv ID") || strings.Contains(header, "Created") {
			_ = f.SetColWidth(sheetName, colLetter, colLetter, 22)
		} else {
			_ = f.SetColWidth(sheetName, colLetter, colLetter, 16)
		}
	}

	// Freeze header row and enable AutoFilter
	lastCol, _ := excelize.ColumnNumberToName(len(headers))
	lastRow := len(rows) + 1
	if lastRow < 2 {
		lastRow = 2
	}
	_ = f.AutoFilter(sheetName, fmt.Sprintf("A1:%s%d", lastCol, lastRow), nil)
	_ = f.SetPanes(sheetName, &excelize.Panes{
		Freeze:      true,
		Split:       false,
		XSplit:      0,
		YSplit:      1,
		TopLeftCell: "A2",
		ActivePane:  "bottomLeft",
	})

	return nil
}

func renderAnalyticsSummarySheet(f *excelize.File, sheetName string, totalMsgs, totalMedia, totalTokens int, stats map[string]any, mediaCounts map[string]int, accounts []map[string]any) error {
	tabColor := "10B981"
	_ = f.SetSheetProps(sheetName, &excelize.SheetPropsOptions{
		TabColorRGB: &tabColor,
	})

	titleStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Family: "Arial", Size: 16, Bold: true, Color: "#1E293B"},
	})

	sectionStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Family: "Arial", Size: 11, Bold: true, Color: "#0F172A"},
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#F1F5F9"}},
		Border: []excelize.Border{
			{Type: "left", Color: "#CBD5E1", Style: 1},
			{Type: "right", Color: "#CBD5E1", Style: 1},
			{Type: "top", Color: "#CBD5E1", Style: 1},
			{Type: "bottom", Color: "#CBD5E1", Style: 1},
		},
	})

	metricLabelStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Family: "Arial", Size: 10, Bold: true, Color: "#334155"},
		Border: []excelize.Border{
			{Type: "left", Color: "#E2E8F0", Style: 1},
			{Type: "right", Color: "#E2E8F0", Style: 1},
			{Type: "top", Color: "#E2E8F0", Style: 1},
			{Type: "bottom", Color: "#E2E8F0", Style: 1},
		},
	})

	metricValStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Family: "Arial", Size: 13, Bold: true, Color: "#1877F2"},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "#E2E8F0", Style: 1},
			{Type: "right", Color: "#E2E8F0", Style: 1},
			{Type: "top", Color: "#E2E8F0", Style: 1},
			{Type: "bottom", Color: "#E2E8F0", Style: 1},
		},
	})

	savingsCardStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Family: "Arial", Size: 13, Bold: true, Color: "#059669"},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "#E2E8F0", Style: 1},
			{Type: "right", Color: "#E2E8F0", Style: 1},
			{Type: "top", Color: "#E2E8F0", Style: 1},
			{Type: "bottom", Color: "#E2E8F0", Style: 1},
		},
	})

	savingsHeaderStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Family: "Arial", Size: 11, Bold: true, Color: "#FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#065F46"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "#A7F3D0", Style: 1},
			{Type: "right", Color: "#A7F3D0", Style: 1},
			{Type: "top", Color: "#A7F3D0", Style: 1},
			{Type: "bottom", Color: "#A7F3D0", Style: 1},
		},
	})

	savingsHighlightStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Family: "Arial", Size: 10, Bold: true, Color: "#065F46"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#ECFDF5"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "#CBD5E1", Style: 1},
			{Type: "right", Color: "#CBD5E1", Style: 1},
			{Type: "top", Color: "#CBD5E1", Style: 1},
			{Type: "bottom", Color: "#CBD5E1", Style: 1},
		},
	})

	tableDataStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Family: "Arial", Size: 10},
		Border: []excelize.Border{
			{Type: "left", Color: "#E2E8F0", Style: 1},
			{Type: "right", Color: "#E2E8F0", Style: 1},
			{Type: "top", Color: "#E2E8F0", Style: 1},
			{Type: "bottom", Color: "#E2E8F0", Style: 1},
		},
	})

	_ = f.SetCellValue(sheetName, "A1", "Free Gemini API — Intelligence & System Analytics")
	_ = f.SetCellStyle(sheetName, "A1", "A1", titleStyle)
	_ = f.SetRowHeight(sheetName, 1, 32)
	_ = f.SetCellValue(sheetName, "A2", fmt.Sprintf("Report Generated: %s | High-Performance Go Engine", time.Now().Format("02 Jan 2006, 03:04 PM")))

	// Official Google Cloud / Vertex AI & Gemini API pricing:
	costTokensUSD := float64(totalTokens) * 0.0000005      // $0.50 per 1M tokens ($0.0000005 per token)
	costImagesUSD := float64(mediaCounts["image"]) * 0.030  // $0.030 per image (Vertex AI Imagen 3)
	costVideosUSD := float64(mediaCounts["video"]) * 1.200  // $1.200 per video (Veo / Gemini video)
	costMusicUSD := float64(mediaCounts["music"]) * 0.080   // $0.080 per track (Audio / Music)
	totalSavedUSD := costTokensUSD + costImagesUSD + costVideosUSD + costMusicUSD
	usdToINR := db.GetUSDToINRRate()
	totalSavedINR := totalSavedUSD * usdToINR

	// KPI Summary Box
	_ = f.SetCellValue(sheetName, "A4", "Core Performance & Financial Metric")
	_ = f.SetCellValue(sheetName, "B4", "Value")
	_ = f.SetCellStyle(sheetName, "A4", "A4", sectionStyle)
	_ = f.SetCellStyle(sheetName, "B4", "B4", sectionStyle)

	totalRequests, _ := stats["total_requests"].(int)
	kpis := []struct {
		Label     string
		Val       string
		IsSavings bool
	}{
		{"Active Primary Model", "Google Gemini 3.8 Flash", false},
		{"Total Chat Messages Logged", fmt.Sprintf("%d", totalMsgs), false},
		{"Total Media Items Generated", fmt.Sprintf("%d", totalMedia), false},
		{"Total Tokens Processed (Est.)", fmt.Sprintf("%d", totalTokens), false},
		{"Total HTTP Requests Logged", fmt.Sprintf("%d", totalRequests), false},
		{"Official API Value Equivalent", fmt.Sprintf("$%.4f USD", totalSavedUSD), false},
		{"Your Total Spend", "$0.00 (Zero API Keys)", false},
		{"Net Cost Saved (USD)", fmt.Sprintf("$%.4f USD (100%% ROI)", totalSavedUSD), true},
		{"Net Cost Saved (INR)", fmt.Sprintf("₹%.2f INR (@ ₹%.1f/$)", totalSavedINR, usdToINR), true},
		{"Active Multi-Account Profiles", fmt.Sprintf("%d", gemini.GetActiveAccountCount()), false},
		{"Connected Extension Workers", fmt.Sprintf("%d", gemini.GetActiveWorkerCount()), false},
		{"Engine Architecture", "HTTP/3 QUIC + Chrome JA4+ + SQLite WAL", false},
	}

	for i, k := range kpis {
		row := 5 + i
		_ = f.SetCellValue(sheetName, fmt.Sprintf("A%d", row), k.Label)
		_ = f.SetCellValue(sheetName, fmt.Sprintf("B%d", row), k.Val)
		_ = f.SetCellStyle(sheetName, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), metricLabelStyle)
		if k.IsSavings {
			_ = f.SetCellStyle(sheetName, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), savingsCardStyle)
		} else {
			_ = f.SetCellStyle(sheetName, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), metricValStyle)
		}
	}

	// ─── Financial Savings Breakdown Table ───────────────────────────────────
	savStartRow := 5 + len(kpis) + 2
	savHeaders := []string{"AI Capability / Service", "Usage Volume", "Official Google API Rate", "Official Cost (USD)", "Official Cost (INR)", "Your Spend", "Net Cost Saved"}
	_ = f.SetRowHeight(sheetName, savStartRow, 26)
	for colIdx, hText := range savHeaders {
		cell, _ := excelize.CoordinatesToCellName(colIdx+1, savStartRow)
		_ = f.SetCellValue(sheetName, cell, hText)
		_ = f.SetCellStyle(sheetName, cell, cell, savingsHeaderStyle)
	}

	savRows := [][]interface{}{
		{"Gemini 3.8 Flash Chat Tokens", fmt.Sprintf("%d Tokens", totalTokens), "$0.50 / 1M Tokens", fmt.Sprintf("$%.4f", costTokensUSD), fmt.Sprintf("₹%.2f", costTokensUSD*usdToINR), "$0.00", fmt.Sprintf("$%.4f (100%%)", costTokensUSD)},
		{"Imagen 3 Ultra-HD Images", fmt.Sprintf("%d Images", mediaCounts["image"]), "$0.030 / Image", fmt.Sprintf("$%.4f", costImagesUSD), fmt.Sprintf("₹%.2f", costImagesUSD*usdToINR), "$0.00", fmt.Sprintf("$%.4f (100%%)", costImagesUSD)},
		{"AI Cinematic Videos (.mp4)", fmt.Sprintf("%d Videos", mediaCounts["video"]), "$1.200 / Video", fmt.Sprintf("$%.4f", costVideosUSD), fmt.Sprintf("₹%.2f", costVideosUSD*usdToINR), "$0.00", fmt.Sprintf("$%.4f (100%%)", costVideosUSD)},
		{"AI Music Synthesis (.mp3)", fmt.Sprintf("%d Tracks", mediaCounts["music"]), "$0.080 / Track", fmt.Sprintf("$%.4f", costMusicUSD), fmt.Sprintf("₹%.2f", costMusicUSD*usdToINR), "$0.00", fmt.Sprintf("$%.4f (100%%)", costMusicUSD)},
		{"🏆 TOTAL FINANCIAL VALUE SAVED", fmt.Sprintf("%d Operations", totalMsgs+totalMedia), "100% Free Engine", fmt.Sprintf("$%.4f", totalSavedUSD), fmt.Sprintf("₹%.2f", totalSavedINR), "$0.00", fmt.Sprintf("$%.4f (100%% ROI)", totalSavedUSD)},
	}

	for rIdx, sRow := range savRows {
		rowNum := savStartRow + 1 + rIdx
		_ = f.SetRowHeight(sheetName, rowNum, 22)
		isTotal := rIdx == len(savRows)-1
		for cIdx, val := range sRow {
			cell, _ := excelize.CoordinatesToCellName(cIdx+1, rowNum)
			_ = f.SetCellValue(sheetName, cell, val)
			if isTotal || cIdx == 6 {
				_ = f.SetCellStyle(sheetName, cell, cell, savingsHighlightStyle)
			} else {
				_ = f.SetCellStyle(sheetName, cell, cell, tableDataStyle)
			}
		}
	}

	// ─── Media Breakdown Table ──────────────────────────────────────────────
	startRow := savStartRow + len(savRows) + 2
	_ = f.SetCellValue(sheetName, fmt.Sprintf("A%d", startRow), "Media Generation Breakdown")
	_ = f.SetCellValue(sheetName, fmt.Sprintf("B%d", startRow), "Count")
	_ = f.SetCellStyle(sheetName, fmt.Sprintf("A%d", startRow), fmt.Sprintf("A%d", startRow), sectionStyle)
	_ = f.SetCellStyle(sheetName, fmt.Sprintf("B%d", startRow), fmt.Sprintf("B%d", startRow), sectionStyle)

	mediaRows := []struct {
		Name  string
		Count int
	}{
		{"Images (Imagen 3 / 8K)", mediaCounts["image"]},
		{"Cinematic Videos (.mp4)", mediaCounts["video"]},
		{"Music Tracks (.mp3)", mediaCounts["music"]},
	}

	for i, mr := range mediaRows {
		row := startRow + 1 + i
		_ = f.SetCellValue(sheetName, fmt.Sprintf("A%d", row), mr.Name)
		_ = f.SetCellValue(sheetName, fmt.Sprintf("B%d", row), mr.Count)
		_ = f.SetCellStyle(sheetName, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), tableDataStyle)
		_ = f.SetCellStyle(sheetName, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), tableDataStyle)
	}

	// ─── Accounts Pool Table ────────────────────────────────────────────────
	accStartRow := startRow + len(mediaRows) + 2
	_ = f.SetCellValue(sheetName, fmt.Sprintf("A%d", accStartRow), "Account Profile ID")
	_ = f.SetCellValue(sheetName, fmt.Sprintf("B%d", accStartRow), "Status")
	_ = f.SetCellValue(sheetName, fmt.Sprintf("C%d", accStartRow), "Total Requests Handled")
	_ = f.SetCellValue(sheetName, fmt.Sprintf("D%d", accStartRow), "Last Used At")
	_ = f.SetCellStyle(sheetName, fmt.Sprintf("A%d", accStartRow), fmt.Sprintf("A%d", accStartRow), sectionStyle)
	_ = f.SetCellStyle(sheetName, fmt.Sprintf("B%d", accStartRow), fmt.Sprintf("B%d", accStartRow), sectionStyle)
	_ = f.SetCellStyle(sheetName, fmt.Sprintf("C%d", accStartRow), fmt.Sprintf("C%d", accStartRow), sectionStyle)
	_ = f.SetCellStyle(sheetName, fmt.Sprintf("D%d", accStartRow), fmt.Sprintf("D%d", accStartRow), sectionStyle)

	for i, acc := range accounts {
		row := accStartRow + 1 + i
		_ = f.SetCellValue(sheetName, fmt.Sprintf("A%d", row), acc["account_id"])
		_ = f.SetCellValue(sheetName, fmt.Sprintf("B%d", row), acc["status"])
		_ = f.SetCellValue(sheetName, fmt.Sprintf("C%d", row), acc["total_requests"])
		_ = f.SetCellValue(sheetName, fmt.Sprintf("D%d", row), acc["last_used_at"])
		_ = f.SetCellStyle(sheetName, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), tableDataStyle)
		_ = f.SetCellStyle(sheetName, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), tableDataStyle)
		_ = f.SetCellStyle(sheetName, fmt.Sprintf("C%d", row), fmt.Sprintf("C%d", row), tableDataStyle)
		_ = f.SetCellStyle(sheetName, fmt.Sprintf("D%d", row), fmt.Sprintf("D%d", row), tableDataStyle)
	}

	_ = f.SetColWidth(sheetName, "A", "A", 36)
	_ = f.SetColWidth(sheetName, "B", "B", 24)
	_ = f.SetColWidth(sheetName, "C", "C", 24)
	_ = f.SetColWidth(sheetName, "D", "D", 22)
	_ = f.SetColWidth(sheetName, "E", "E", 22)
	_ = f.SetColWidth(sheetName, "F", "F", 18)
	_ = f.SetColWidth(sheetName, "G", "G", 22)

	return nil
}
