package dto

import (
	"sort"
	"time"

	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
)

// The renderers below produce EXACTLY the shapes the frontend report pages
// declare (compliance-heatmap / compliance-status-report / tax-financial-reports
// / export-raw-data .tsx) so the pages need no edits. Slices are always
// non-nil so an empty tenant serializes as [] — never null.

type idLabel struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// HeatmapToJSON derives rows (first-seen order over the cells, which arrive
// sorted by row label then calendar), cols (ordered globally by the earliest
// period they contain, then label — so M2 precedes M10 whichever row
// introduces it), cell statuses and the summary.
func HeatmapToJSON(cells []domain.HeatmapCell) map[string]any {
	rows := []idLabel{}
	cols := []idLabel{}
	seenRow := map[string]bool{}
	colFirst := map[string]time.Time{}
	out := make([]map[string]any, 0, len(cells))
	summary := map[string]int{"totalCells": len(cells), "green": 0, "amber": 0, "red": 0}

	for i := range cells {
		c := &cells[i]
		if !seenRow[c.RowID] {
			seenRow[c.RowID] = true
			rows = append(rows, idLabel{ID: c.RowID, Label: c.RowLabel})
		}
		if first, seen := colFirst[c.ColID]; !seen {
			colFirst[c.ColID] = c.FirstPeriodEnd
			cols = append(cols, idLabel{ID: c.ColID, Label: c.ColLabel})
		} else if c.FirstPeriodEnd.Before(first) {
			colFirst[c.ColID] = c.FirstPeriodEnd
		}
		status := c.Status()
		if _, counted := summary[status]; counted {
			summary[status]++
		}
		out = append(out, map[string]any{
			"rowId":           c.RowID,
			"rowLabel":        c.RowLabel,
			"colId":           c.ColID,
			"colLabel":        c.ColLabel,
			"status":          status,
			"totalTasks":      c.TotalTasks,
			"completedTasks":  c.CompletedTasks,
			"overdueTasks":    c.OverdueTasks,
			"inProgressTasks": c.InProgressTasks,
			"workflowIds":     c.WorkflowIDList(),
		})
	}
	sort.SliceStable(cols, func(i, j int) bool {
		a, b := colFirst[cols[i].ID], colFirst[cols[j].ID]
		if !a.Equal(b) {
			return a.Before(b)
		}
		return cols[i].Label < cols[j].Label
	})
	return map[string]any{"rows": rows, "cols": cols, "cells": out, "summary": summary}
}

func ComplianceStatusToJSON(rows []domain.ComplianceRow, summary domain.ComplianceSummary) map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, map[string]any{
			"entityName":       r.EntityName,
			"entityId":         r.EntityID,
			"taxType":          r.TaxType,
			"obligationName":   r.ObligationName,
			"obligationCode":   r.ObligationCode,
			"period":           r.Period,
			"filingDeadline":   r.FilingDeadline,
			"filingDate":       formatInstant(r.CompletedAt),
			"complianceStatus": r.ComplianceStatus,
			"penaltyInterest":  r.PenaltyInterest,
			"workflowId":       r.WorkflowID,
			"taskInstanceId":   r.TaskInstanceID,
		})
	}
	return map[string]any{
		"rows": out,
		"summary": map[string]any{
			"total":  summary.Total,
			"onTime": summary.OnTime,
			"late":   summary.Late,
			"missed": summary.Missed,
			"notDue": summary.NotDue,
		},
		"totalCount": summary.Total,
	}
}

func figuresJSON(f *domain.Figures, m map[string]any) map[string]any {
	m["outputVat"] = f.OutputVat
	m["inputVat"] = f.InputVat
	m["netVat"] = f.NetVat
	m["taxableIncome"] = f.TaxableIncome
	m["taxLiability"] = f.TaxLiability
	m["whtAmount"] = f.WhtAmount
	m["engagementCost"] = f.EngagementCost
	m["totalAmount"] = f.TotalAmount
	return m
}

func TaxFinancialToJSON(res *domain.TaxFinancialResult) map[string]any {
	rows := make([]map[string]any, 0, len(res.Rows))
	for i := range res.Rows {
		r := &res.Rows[i]
		rows = append(rows, figuresJSON(&r.Figures, map[string]any{
			"entityName":     r.EntityName,
			"entityId":       r.EntityID,
			"country":        r.Country,
			"taxType":        r.TaxType,
			"obligationName": r.ObligationName,
			"obligationCode": r.ObligationCode,
			"period":         r.Period,
			"financialYear":  r.FinancialYear,
		}))
	}
	aggregated := make([]map[string]any, 0, len(res.Aggregated))
	for i := range res.Aggregated {
		g := &res.Aggregated[i]
		aggregated = append(aggregated, figuresJSON(&g.Figures, map[string]any{
			"key": g.Key, "label": g.Label, "count": g.Count,
		}))
	}
	chart := make([]map[string]any, 0, len(res.ChartData))
	for i := range res.ChartData {
		p := &res.ChartData[i]
		chart = append(chart, map[string]any{
			"period":       p.Period,
			"outputVat":    p.OutputVat,
			"inputVat":     p.InputVat,
			"netVat":       p.NetVat,
			"taxLiability": p.TaxLiability,
			"whtAmount":    p.WhtAmount,
			"totalAmount":  p.TotalAmount,
		})
	}
	s := &res.Summary
	return map[string]any{
		"rows":       rows,
		"aggregated": aggregated,
		"chartData":  chart,
		"summary": map[string]any{
			"totalOutputVat":      s.OutputVat,
			"totalInputVat":       s.InputVat,
			"totalNetVat":         s.NetVat,
			"totalTaxableIncome":  s.TaxableIncome,
			"totalTaxLiability":   s.TaxLiability,
			"totalWht":            s.WhtAmount,
			"totalEngagementCost": s.EngagementCost,
			"totalAmount":         s.TotalAmount,
			"recordCount":         s.RecordCount,
		},
		"totalCount": res.TotalCount,
	}
}

// str renders an optional text column as "" (the legacy export's `|| ""`).
func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// instantOrEmpty renders an optional instant as "" when absent (legacy export).
func instantOrEmpty(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(instantLayout)
}

// Export*ToJSON emit exactly the column keys export-raw-data.tsx declares
// (workflowColumns / taskColumns / taxDataColumns).
func ExportWorkflowsToJSON(rows []domain.ExportWorkflowRow) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, map[string]any{
			"workflowName":    r.Name,
			"category":        r.Category,
			"projectType":     str(r.ProjectType),
			"financialYear":   str(r.FinancialYear),
			"periodicity":     str(r.Periodicity),
			"entityName":      str(r.EntityName),
			"country":         str(r.Country),
			"obligationName":  str(r.ObligationName),
			"obligationCode":  str(r.ObligationCode),
			"taxType":         str(r.TaxType),
			"status":          r.Status,
			"startDate":       str(r.StartDate),
			"endDate":         str(r.EndDate),
			"tasksSequential": r.TasksSequential,
			"createdAt":       r.CreatedAt.UTC().Format(instantLayout),
		})
	}
	return out
}

func ExportTasksToJSON(rows []domain.ExportTaskRow) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, map[string]any{
			"taskName":         r.Name,
			"taskType":         r.TaskType,
			"status":           r.Status,
			"workflowName":     r.WorkflowName,
			"workflowCategory": r.WorkflowCategory,
			"entityName":       str(r.EntityName),
			"country":          str(r.Country),
			"obligationName":   str(r.ObligationName),
			"taxType":          str(r.TaxType),
			"periodCode":       r.PeriodCode,
			"financialYear":    str(r.FinancialYear),
			"assigneeName":     str(r.AssigneeName),
			"dueDate":          r.DueDate,
			"filingDeadline":   r.FilingDeadline,
			"completedAt":      instantOrEmpty(r.CompletedAt),
			"approvalRequired": r.ApprovalRequired,
			"taxDataStatus":    r.TaxDataStatus,
		})
	}
	return out
}

func ExportTaxDataToJSON(rows []domain.ExportTaxDataRow) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, map[string]any{
			"taskName":       r.Name,
			"workflowName":   r.WorkflowName,
			"entityName":     str(r.EntityName),
			"country":        str(r.Country),
			"obligationName": str(r.ObligationName),
			"taxType":        str(r.TaxType),
			"periodCode":     r.PeriodCode,
			"financialYear":  str(r.FinancialYear),
			"taxDataStatus":  r.TaxDataStatus,
			"outputVat":      r.OutputVat,
			"inputVat":       r.InputVat,
			"netVat":         r.NetVat,
			"taxableIncome":  r.TaxableIncome,
			"taxLiability":   r.TaxLiability,
			"whtAmount":      r.WhtAmount,
			"penaltyAmount":  r.PenaltyAmount,
			"interestAmount": r.InterestAmount,
			"engagementCost": r.EngagementCost,
		})
	}
	return out
}

func ExportToJSON(dataset string, rows []map[string]any, total int) map[string]any {
	return map[string]any{"dataset": dataset, "rows": rows, "totalCount": total}
}
