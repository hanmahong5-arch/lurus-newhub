package handler

// faultsim_decision_test.go - the simulator's System One wire in success mode
// (model "ok-decision"): a choice question is answered with a fixed
// distribution steered by keywords in the criteria text, so decision routing
// can be driven end to end in UAT without a vendor key.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func TestFaultSimOK_SystemOneDecision(t *testing.T) {
	t.Setenv("FAULTSIM_TOKEN", "tok")
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.POST("/v1/systemone", FaultSimSystemOne)
	auth := map[string]string{"X-Faultsim-Token": "tok"}

	body := func(cheapCriteria string) string {
		return `{"model":"ok-decision","state":"hello","questions":{"route":{"type":"choice","instructions":"pick","criteria":{` +
			`"cheap":"` + cheapCriteria + `","strong":"hard problems","no_preference":"unclear"}}}}`
	}

	t.Run("keyword cheap gets 0.8 and the distribution sums to 1", func(t *testing.T) {
		w := faultSimDoPost(e, "/v1/systemone", body("cheap and simple"), auth)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		a := gjson.Get(w.Body.String(), "answers.route")
		if a.Get("choice").String() != "cheap" || a.Get("probabilities.cheap").Float() != 0.8 {
			t.Fatalf("answer = %s", a.Raw)
		}
		sum := 0.0
		a.Get("probabilities").ForEach(func(_, v gjson.Result) bool { sum += v.Float(); return true })
		if sum < 0.999 || sum > 1.001 {
			t.Fatalf("probabilities sum to %v: %s", sum, a.Raw)
		}
		if gjson.Get(w.Body.String(), "usage.input_tokens").Int() != 1000 {
			t.Fatalf("usage missing: %s", w.Body)
		}
	})

	t.Run("keyword vague gets 0.4", func(t *testing.T) {
		w := faultSimDoPost(e, "/v1/systemone", body("vague things"), auth)
		if got := gjson.Get(w.Body.String(), "answers.route.probabilities.cheap").Float(); got != 0.4 {
			t.Fatalf("cheap probability = %v: %s", got, w.Body)
		}
	})

	t.Run("without the token the simulator refuses", func(t *testing.T) {
		w := faultSimDoPost(e, "/v1/systemone", body("cheap"), nil)
		if w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), "answers") {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})
}
