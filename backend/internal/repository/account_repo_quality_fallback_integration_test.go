//go:build integration

package repository

import "testing"

func TestListGroupQualityFallbackCandidatesIncludesAutomaticHoldsIntegration(t *testing.T) {
	tx := testEntTx(t)
	assertGroupQualityFallbackCandidateList(t, tx.Client(), newAccountRepositoryWithSQL(tx.Client(), tx, nil))
}
