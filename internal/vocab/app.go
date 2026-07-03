package vocab

import "io"

func Export(configPath string, stdout io.Writer) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}
	if err := validateConfig(cfg); err != nil {
		return err
	}

	records, skippedMissingUsage, err := ReadKindleRecords(cfg.Kindle.VocabDB)
	if err != nil {
		return err
	}
	deduped := DeduplicateRecords(records)
	summary := Summary{
		RawRecords:          len(records) + skippedMissingUsage,
		SkippedMissingUsage: skippedMissingUsage,
		DedupedRecords:      len(deduped),
	}

	lookups, reviews, lookupFailures, err := LookupFlashDictSenses(cfg, deduped)
	if err != nil {
		return err
	}
	summary.LookupFailures = lookupFailures

	cards, aiReviews, aiLowConfidence, aiInvalid, err := SelectAndBuildCards(cfg, deduped, lookups)
	if err != nil {
		return err
	}
	reviews = append(reviews, aiReviews...)
	summary.AILowConfidence = aiLowConfidence
	summary.AIInvalid = aiInvalid
	summary.Cards = len(cards)
	summary.ReviewRecords = len(reviews)

	if err := WriteFlashcardEnvelope(cfg.Output.Path, cards); err != nil {
		return err
	}
	if err := WriteJSONL(cfg.Output.ReviewPath, reviews); err != nil {
		return err
	}
	PrintSummary(stdout, summary, cfg)
	return nil
}
