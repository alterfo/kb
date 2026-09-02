package retriever

import "context"

// FeedbackPrior supplies a per-document personal prior derived from user
// feedback. It is optional: a nil value disables the prior. Satisfied by
// history stores that aggregate thumbs-up/down ratings per document.
type FeedbackPrior interface {
	FeedbackByDoc(ctx context.Context) (map[string]float64, error)
}

// feedbackPrior returns the feedback score for a document scaled by the
// configured bonus, or zero when feedback is disabled or unavailable
// (fail-open: a broken prior must never break retrieval).
func feedbackPrior(prior map[string]float64, bonus float64, docID string) float64 {
	if bonus == 0 || len(prior) == 0 || docID == "" {
		return 0
	}
	return prior[docID] * bonus
}

// personalPrior loads the per-document feedback prior once per fusion pass.
// A nil Feedback provider or a lookup error disables the prior rather than
// failing the query.
func (r *Retriever) personalPrior(ctx context.Context) map[string]float64 {
	if r.cfg.Feedback == nil || r.cfg.FeedbackBonus == 0 {
		return nil
	}
	prior, err := r.cfg.Feedback.FeedbackByDoc(ctx)
	if err != nil {
		addDegraded(ctx, "feedback prior unavailable: "+err.Error())
		return nil
	}
	return prior
}
