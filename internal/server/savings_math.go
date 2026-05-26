package server

const savingsMathBaselineMethod = "full_file_read"
const savingsMathFormula = "baseline_tokens=tokens_used+tokens_saved; savings_pct=tokens_saved/baseline_tokens*100"

func buildSavingsMath(tokensUsed, tokensSaved int64) map[string]any {
	baseline := tokensUsed + tokensSaved
	var savingsPct float64
	var usedPct float64
	var compressionRatio float64
	if baseline > 0 {
		savingsPct = float64(tokensSaved) / float64(baseline) * 100
		usedPct = float64(tokensUsed) / float64(baseline) * 100
	}
	if tokensUsed > 0 {
		compressionRatio = float64(baseline) / float64(tokensUsed)
	}
	return map[string]any{
		"baseline_method":   savingsMathBaselineMethod,
		"formula":           savingsMathFormula,
		"tokens_used":       tokensUsed,
		"tokens_saved":      tokensSaved,
		"baseline_tokens":   baseline,
		"savings_pct":       savingsPct,
		"used_pct":          usedPct,
		"compression_ratio": compressionRatio,
	}
}

func savingsImprovementSignal(tokensUsed int64, tokensSaved *int64) string {
	if tokensSaved == nil {
		return "needs_baseline"
	}
	baseline := tokensUsed + *tokensSaved
	if baseline <= 0 {
		return "no_usage"
	}
	pct := float64(*tokensSaved) / float64(baseline) * 100
	switch {
	case pct >= 70:
		return "excellent"
	case pct >= 50:
		return "good"
	case pct >= 30:
		return "watch"
	default:
		return "improve_query"
	}
}
