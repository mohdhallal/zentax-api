package spec

import (
	"strconv"
	"time"
)

// DefaultCompletionLocalTime is the local clock time a completion is stamped
// at when the dataset does not override it.
const DefaultCompletionLocalTime = "10:00"

// CompletionLocalTime is the dataset-wide default completion time (HH:MM).
func (s *Spec) CompletionLocalTime() string {
	if s.Conventions.CompletionLocalTime == "" {
		return DefaultCompletionLocalTime
	}
	return s.Conventions.CompletionLocalTime
}

// LocalCompletionTime is the clock time this instance is completed at, its own
// override or the dataset default.
func (i Instance) LocalCompletionTime(datasetDefault string) string {
	if i.CompletedAtLocalTime != "" {
		return i.CompletedAtLocalTime
	}
	if datasetDefault == "" {
		return DefaultCompletionLocalTime
	}
	return datasetDefault
}

// CompletionInstant is the instant the completion escape hatch must write for
// this instance: its civil completion date at its local completion time, read
// in the tenant's zone. ok is false when the instance declares no completion.
//
// This is the dataset's own stamping rule (see $schemaNotes.conventions), not a
// report rule: how a completion is later CLASSIFIED belongs to the verifier.
func (i Instance) CompletionInstant(zone *time.Location, datasetDefault string) (time.Time, bool) {
	if i.CompletedOn.IsZero() || zone == nil {
		return time.Time{}, false
	}
	hour, minute, ok := parseClockTime(i.LocalCompletionTime(datasetDefault))
	if !ok {
		return time.Time{}, false
	}
	return time.Date(
		i.CompletedOn.Year, time.Month(i.CompletedOn.Month), i.CompletedOn.Day,
		hour, minute, 0, 0, zone,
	).UTC(), true
}

// parseClockTime reads an HH:MM value.
func parseClockTime(value string) (hour, minute int, ok bool) {
	if len(value) != 5 || value[2] != ':' {
		return 0, 0, false
	}
	hour, err := strconv.Atoi(value[:2])
	if err != nil || hour < 0 || hour > 23 {
		return 0, 0, false
	}
	minute, err = strconv.Atoi(value[3:])
	if err != nil || minute < 0 || minute > 59 {
		return 0, 0, false
	}
	return hour, minute, true
}

// Cross-references in the dataset are keys, not ids. These lookups resolve
// them; the seeder keeps its own key → server-id map on top.

// Tenant returns the tenant with this key.
func (s *Spec) Tenant(key string) (Tenant, bool) {
	for _, tenant := range s.Tenants {
		if tenant.Key == key {
			return tenant, true
		}
	}
	return Tenant{}, false
}

// AllUsers returns the admin followed by the invited members.
func (t *Tenant) AllUsers() []User {
	users := make([]User, 0, len(t.Users)+1)
	users = append(users, t.Admin)
	return append(users, t.Users...)
}

// User resolves a user key (the admin included).
func (t *Tenant) User(key string) (User, bool) {
	if t.Admin.Key == key {
		return t.Admin, true
	}
	for _, user := range t.Users {
		if user.Key == key {
			return user, true
		}
	}
	return User{}, false
}

// Entity resolves an entity key.
func (t *Tenant) Entity(key string) (Entity, bool) {
	for _, entity := range t.Entities {
		if entity.Key == key {
			return entity, true
		}
	}
	return Entity{}, false
}

// ObligationType resolves an obligation-type key.
func (t *Tenant) ObligationType(key string) (ObligationType, bool) {
	for _, obligation := range t.ObligationTypes {
		if obligation.Key == key {
			return obligation, true
		}
	}
	return ObligationType{}, false
}

// EntityObligation resolves an entity-obligation key.
func (t *Tenant) EntityObligation(key string) (EntityObligation, bool) {
	for _, entityObligation := range t.EntityObligations {
		if entityObligation.Key == key {
			return entityObligation, true
		}
	}
	return EntityObligation{}, false
}

// Workflow resolves a workflow key.
func (t *Tenant) Workflow(key string) (Workflow, bool) {
	for _, workflow := range t.Workflows {
		if workflow.Key == key {
			return workflow, true
		}
	}
	return Workflow{}, false
}

// Template resolves a task-template key inside this workflow.
func (w *Workflow) Template(key string) (TaskTemplate, bool) {
	for _, template := range w.Templates {
		if template.Key == key {
			return template, true
		}
	}
	return TaskTemplate{}, false
}
