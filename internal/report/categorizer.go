package report

import "sort"

type CategorizedFindings struct {
	Blocking []Finding
	Critical []Finding
	Warning  []Finding
	Info     []Finding
	// Unverified collects low-severity (WARNING/INFO) findings whose
	// confidence is "uncertain". They are pulled out of their severity bucket
	// so the main report stays high-signal; the renderer shows them in a
	// dedicated low-confidence section. BLOCKING/CRITICAL findings are never
	// demoted here — they are too important to bury even when uncertain.
	Unverified []Finding
}

// Categorize buckets findings by severity, applying the minSeverity and
// minConfidence thresholds. Within every bucket findings are ordered by
// confidence (verified first). Uncertain WARNING/INFO findings are routed to
// the Unverified bucket instead of their severity bucket.
func Categorize(findings []Finding, minSeverity Severity, minConfidence Confidence) CategorizedFindings {
	var cf CategorizedFindings
	for _, f := range findings {
		if _, ok := severityOrder[f.Severity]; !ok {
			continue // skip unknown severity
		}
		if !f.Severity.MeetsMinimum(minSeverity) {
			continue
		}
		if !f.Confidence.MeetsMinimum(minConfidence) {
			continue
		}
		// Low-confidence, low-severity findings are demoted to the Unverified
		// section so an uncertain nit never sits next to a verified bug.
		if f.Confidence == ConfidenceUncertain &&
			(f.Severity == SeverityWarning || f.Severity == SeverityInfo) {
			cf.Unverified = append(cf.Unverified, f)
			continue
		}
		switch f.Severity {
		case SeverityBlocking:
			cf.Blocking = append(cf.Blocking, f)
		case SeverityCritical:
			cf.Critical = append(cf.Critical, f)
		case SeverityWarning:
			cf.Warning = append(cf.Warning, f)
		case SeverityInfo:
			cf.Info = append(cf.Info, f)
		}
	}
	sortByConfidence(cf.Blocking)
	sortByConfidence(cf.Critical)
	sortByConfidence(cf.Warning)
	sortByConfidence(cf.Info)
	sortByConfidence(cf.Unverified)
	return cf
}

// sortByConfidence stably orders findings strongest-confidence-first, leaving
// equal-confidence findings in their original (severity-assigned) order.
func sortByConfidence(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		return findings[i].Confidence.Rank() < findings[j].Confidence.Rank()
	})
}

func (cf CategorizedFindings) Total() int {
	return len(cf.Blocking) + len(cf.Critical) + len(cf.Warning) + len(cf.Info) + len(cf.Unverified)
}

func (cf CategorizedFindings) HasBlockersOrCritical() bool {
	return len(cf.Blocking) > 0 || len(cf.Critical) > 0
}
