package app

import "testing"

func TestStickyRolloutMonotonicAndDisabled(t *testing.T) {
	for _, viewer := range []string{"resident-a", "resident-b", "anonymous-browser", ""} {
		if stickyRecommendation(viewer, 0) || !stickyRecommendation(viewer, 100) {
			t.Fatal("rollout bounds")
		}
		prior := false
		for _, percent := range []int{1, 5, 25, 100} {
			got := stickyRecommendation(viewer, percent)
			if prior && !got {
				t.Fatal("sticky assignment moved out during expansion")
			}
			if got != stickyRecommendation(viewer, percent) {
				t.Fatal("unstable assignment")
			}
			prior = got
		}
	}
}
