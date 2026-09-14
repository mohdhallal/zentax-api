package domain

// vocabulary is one of the API's enums plus the spellings a customer's file
// writes for it. allowed is the API's own list, in the order a message names
// them; synonyms are the extra spellings, keyed by their normalised form.
//
// Every allowed value is itself matched through NormalizeHeader, so the
// punctuation and case a file uses cost nothing: "Bi-Annual", "bi annual" and
// "BIANNUAL" are all the API's `bi-annual` without a synonym being listed.
type vocabulary struct {
	allowed  []string
	synonyms map[string]string
}

func newVocabulary(allowed []string, synonyms map[string]string) vocabulary {
	index := make(map[string]string, len(allowed)+len(synonyms))
	for _, value := range allowed {
		index[NormalizeHeader(value)] = value
	}
	for spelling, value := range synonyms {
		index[NormalizeHeader(spelling)] = value
	}
	return vocabulary{allowed: allowed, synonyms: index}
}

func (v vocabulary) lookup(raw string) (string, bool) {
	value, ok := v.synonyms[NormalizeHeader(raw)]
	return value, ok
}

// The vocabularies, each one copied from the validate tag of the single-record
// body it belongs to, with the spellings a spreadsheet writes added on top.
var (
	// periodicityVocab is entityobligations/dto.CreateEntityObligationBody's
	// `oneof` for periodicity.
	periodicityVocab = newVocabulary(
		[]string{"weekly", "monthly", "quarterly", "bi-annual", "annual", "consolidated-annual"},
		map[string]string{
			"week": "weekly", "every week": "weekly",
			"month": "monthly", "every month": "monthly", "m": "monthly",
			"quarter": "quarterly", "every quarter": "quarterly", "q": "quarterly", "3 monthly": "quarterly",
			"semi-annual": "bi-annual", "semiannually": "bi-annual", "half-yearly": "bi-annual",
			"half year": "bi-annual", "6 monthly": "bi-annual", "twice yearly": "bi-annual",
			"year": "annual", "yearly": "annual", "annually": "annual", "a": "annual",
			"consolidated": "consolidated-annual", "consolidated annual return": "consolidated-annual",
		},
	)

	// fiscalPatternVocab is entities/dto.CreateEntityBody's `oneof` for
	// fiscalCalendarPattern.
	fiscalPatternVocab = newVocabulary(
		[]string{"standard", "445", "454", "544", "13-period", "weekly", "custom"},
		map[string]string{
			"calendar": "standard", "gregorian": "standard", "normal": "standard", "monthly": "standard",
			"4-4-5": "445", "4 4 5": "445",
			"4-5-4": "454", "4 5 4": "454",
			"5-4-4": "544", "5 4 4": "544",
			"13 periods": "13-period", "13 period": "13-period", "thirteen period": "13-period",
			"52/53 week": "weekly", "weeks": "weekly",
		},
	)

	// weekEndDayVocab is entities/dto.CreateEntityBody's `oneof` for
	// fiscalWeekEndDay.
	weekEndDayVocab = newVocabulary(
		[]string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"},
		map[string]string{
			"mon": "monday", "tue": "tuesday", "tues": "tuesday", "wed": "wednesday",
			"thu": "thursday", "thur": "thursday", "thurs": "thursday",
			"fri": "friday", "sat": "saturday", "sun": "sunday",
		},
	)

	// yearEndRuleVocab is entities/dto.CreateEntityBody's `oneof` for
	// fiscalYearEndRule.
	yearEndRuleVocab = newVocabulary(
		[]string{"last", "nearest"},
		map[string]string{"closest": "nearest", "nearest to": "nearest", "last weekday": "last"},
	)

	// deadlineTypeVocab is entityobligations/domain.DeadlineRule's `oneof` for
	// type.
	deadlineTypeVocab = newVocabulary(
		[]string{"fixed", "period_offset"},
		map[string]string{
			"fixed dates": "fixed", "calendar dates": "fixed", "fixed date": "fixed",
			"offset": "period_offset", "period offset": "period_offset",
			"after period end": "period_offset", "relative": "period_offset",
		},
	)

	// weekendAdjustmentVocab is entityobligations/domain.DeadlineRule's `oneof`
	// for weekendAdjustment.
	weekendAdjustmentVocab = newVocabulary(
		[]string{"none", "next-business-day", "prev-business-day"},
		map[string]string{
			"no": "none", "never": "none", "keep": "none",
			"next working day": "next-business-day", "next": "next-business-day",
			"forward": "next-business-day", "roll forward": "next-business-day",
			"previous business day": "prev-business-day", "previous working day": "prev-business-day",
			"previous": "prev-business-day", "back": "prev-business-day", "roll back": "prev-business-day",
		},
	)
)
