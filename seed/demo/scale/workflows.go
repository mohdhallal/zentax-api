package scale

import (
	"fmt"
	"strconv"

	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
)

// workflows builds the grid — every entity × obligation type × fiscal year,
// the years ending with the entity's fiscal year running at AsOf — starts
// each workflow, draws the state of every instance, sprinkles a few
// completed / archived workflows among those entirely in the past, and adds
// the never-started drafts for the next fiscal year.
func (g *generator) workflows(t *spec.Tenant) error {
	for _, e := range g.entitiesOut {
		lastFY := currentFinancialYear(g.cfg.AsOf, e.spec.FinancialYearEnd)
		for _, ot := range obligationTypeDefs {
			ruleKey := entityObligationKey(e.spec.Key, ot.key)
			rule, ok := t.EntityObligation(ruleKey)
			if !ok {
				return fmt.Errorf("scale: no entity obligation %s", ruleKey)
			}
			for fy := lastFY - g.cfg.Years + 1; fy <= lastFY; fy++ {
				w, err := g.workflow(e, ot, fy, true)
				if err != nil {
					return err
				}
				plan, err := planWorkflow(e.spec, &rule.DeadlineRule, w, fy)
				if err != nil {
					return err
				}
				g.materialize(&w, plan, ot, e)
				t.Workflows = append(t.Workflows, w)
			}
		}
	}

	// Drafts: next year's workflow prepared but not started, one per
	// obligation type on the first entities.
	for i, ot := range obligationTypeDefs {
		e := g.entitiesOut[i%len(g.entitiesOut)]
		fy := currentFinancialYear(g.cfg.AsOf, e.spec.FinancialYearEnd) + 1
		w, err := g.workflow(e, ot, fy, false)
		if err != nil {
			return err
		}
		g.stats.ByWorkflowStatus[w.Lifecycle.FinalStatus]++
		g.stats.WorkflowTasks += len(w.Templates)
		t.Workflows = append(t.Workflows, w)
	}
	return nil
}

// workflow renders one workflow row (no instances yet).
func (g *generator) workflow(e entity, ot obligationTypeDef, fy int, start bool) (spec.Workflow, error) {
	startDate, endDate, err := fiscalYearBounds(e.spec, fy)
	if err != nil {
		return spec.Workflow{}, fmt.Errorf("scale: %s FY%d: %w", e.spec.Key, fy, err)
	}
	entityKey := e.spec.Key
	otKey := g.cfg.Slug + "." + ot.key
	eoKey := entityObligationKey(e.spec.Key, ot.key)
	periodicity := ot.periodicity
	year := strconv.Itoa(fy)
	w := spec.Workflow{
		Key:              fmt.Sprintf("%s.%s.%d", e.spec.Key, ot.key, fy),
		Name:             fmt.Sprintf("%s %s FY%d", e.spec.Name, ot.code, fy),
		Description:      fmt.Sprintf("%s for %s, fiscal year ending %s", ot.name, e.spec.Name, endDate),
		Category:         spec.CategoryRecurring,
		Entity:           &entityKey,
		ObligationType:   &otKey,
		EntityObligation: &eoKey,
		FinancialYear:    year,
		Periodicity:      &periodicity,
		SelectedPeriods:  append([]string(nil), ot.periods...),
		DueDateRule:      dueDateRuleFor(e, ot),
		StartDate:        startDate,
		EndDate:          endDate,
		Writer:           g.writers[g.rng.IntN(len(g.writers))],
		Approver:         g.approvers[g.rng.IntN(len(g.approvers))],
		Lifecycle:        spec.Lifecycle{Start: start, FinalStatus: "active"},
		Templates:        specTemplates(ot),
	}
	if !start {
		w.Lifecycle.FinalStatus = "draft"
	}
	return w, nil
}

// materialize draws the instance states of a started workflow and records
// the plan and the statistics. A workflow whose every instance is more than
// 60 days in the past may be sprinkled `completed` (every instance then
// completes) or `archived` (states as drawn; archived workflows leave the
// compliance reports).
func (g *generator) materialize(w *spec.Workflow, plan []PlannedInstance, ot obligationTypeDef, e entity) {
	allFarPast := true
	for _, p := range plan {
		if bandOf(p.DueDate, g.cfg.AsOf) != BandFarPast {
			allFarPast = false
			break
		}
	}
	if allFarPast {
		switch roll := g.rng.Float64(); {
		case roll < 0.012:
			w.Lifecycle.FinalStatus = "completed"
		case roll < 0.024:
			w.Lifecycle.FinalStatus = "archived"
		}
	}
	forceCompleted := w.Lifecycle.FinalStatus == "completed"
	g.stats.ByWorkflowStatus[w.Lifecycle.FinalStatus]++
	g.stats.WorkflowsStarted++
	g.stats.WorkflowTasks += len(w.Templates)

	templates := map[string]templateDef{}
	for _, td := range ot.templates {
		templates[td.key] = td
	}
	weekEnd := endOfWeek(g.cfg.AsOf)
	for i := range plan {
		p := &plan[i]
		td := templates[p.TemplateKey]
		inst, listed := g.instanceState(*p, ot, td, e, forceCompleted)
		p.Band = bandOf(p.DueDate, g.cfg.AsOf)
		p.Status = inst.Status
		if listed {
			w.Instances = append(w.Instances, inst)
			g.stats.Deviations++
		}

		// Statistics, with the oracle's rules.
		g.stats.Instances++
		g.stats.ByStatus[inst.Status]++
		g.stats.ByBand[p.Band]++
		g.stats.Classification[classify(inst, p.DueDate, g.cfg.AsOf)]++
		if inst.Assignee != "" {
			g.stats.Assigned++
		}
		if len(inst.TaxData) > 0 {
			g.stats.WithTaxData++
		}
		if inst.Notes != "" {
			g.stats.WithNotes++
		}
		if inst.Status == statusPendingApproval {
			g.stats.AwaitingApproval++
		}
		if inst.Status == statusCompleted {
			g.stats.Completed++
			continue
		}
		g.stats.Open++
		if inst.Assignee != "" {
			g.stats.AssignedOpen++
		}
		switch {
		case compareDates(p.DueDate, g.cfg.AsOf) < 0:
			g.stats.Overdue++
		case compareDates(p.DueDate, g.cfg.AsOf) == 0:
			g.stats.DueToday++
		case compareDates(p.DueDate, weekEnd) <= 0:
			g.stats.ThisWeek++
		}
	}
	g.plan = append(g.plan, plan...)
}
