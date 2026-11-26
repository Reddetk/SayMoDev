//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestMigratePatientsEndpoint(t *testing.T) {
	admin := tokenForRole(t, "administrator")

	createBody := map[string]string{
		"program": fmt.Sprintf(`{"title":"E2E migration","classifier":{"difficulty":"medium","aphasiaType":%q}}`, "e2e_"+uuid.NewString()[:8]),
		"lessons": `[{"title":"Первый"},{"title":"Второй"}]`,
	}
	resp, body := doMultipart(t, baseURL()+"/content/programs", admin, createBody, map[string][]byte{
		"lesson_0": []byte(testPDF),
		"lesson_1": []byte(testPDF),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d (%s)", resp.StatusCode, body)
	}
	var created struct {
		ProgramID string `json:"programId"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("create body: %v", err)
	}
	t.Cleanup(func() {
		_, _ = doJSON(t, http.MethodDelete, baseURL()+"/content/programs/"+created.ProgramID, admin, nil)
	})

	// target_version=1 существует (create даёт v1); specific требует непустой UUID.
	payload := map[string]any{
		"target_version":    1,
		"patient_selection": "specific",
		"account_ids":       []string{uuid.NewString()},
	}
	resp, body = doJSON(t, http.MethodPost, baseURL()+"/admin/content/programs/"+created.ProgramID+"/migrate-patients", admin, payload)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("migrate: want 202, got %d (%s)", resp.StatusCode, body)
	}
	var accepted struct {
		JobID    string `json:"migration_job_id"`
		Affected int    `json:"affected_patients_count"`
	}
	if err := json.Unmarshal([]byte(body), &accepted); err != nil || accepted.JobID == "" || accepted.Affected != 1 {
		t.Fatalf("accepted body: %v (%s)", err, body)
	}

	// Валидация: пустой account_ids → 400; неизвестная версия → 404.
	payload["account_ids"] = []string{}
	resp, body = doJSON(t, http.MethodPost, baseURL()+"/admin/content/programs/"+created.ProgramID+"/migrate-patients", admin, payload)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty accounts: want 400, got %d (%s)", resp.StatusCode, body)
	}
	payload["account_ids"] = []string{uuid.NewString()}
	payload["target_version"] = 99
	resp, body = doJSON(t, http.MethodPost, baseURL()+"/admin/content/programs/"+created.ProgramID+"/migrate-patients", admin, payload)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("target 99: want 404, got %d (%s)", resp.StatusCode, body)
	}

	// 10/мин на администратора: добиваем окно до 429.
	payload["target_version"] = 1
	got429 := false
	for i := 0; i < 15; i++ {
		resp, body = doJSON(t, http.MethodPost, baseURL()+"/admin/content/programs/"+created.ProgramID+"/migrate-patients", admin, payload)
		if resp.StatusCode == http.StatusTooManyRequests {
			got429 = true
			break
		}
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("rate limit loop: unexpected %d (%s)", resp.StatusCode, body)
		}
	}
	if !got429 {
		t.Fatal("rate limit 10/min was not enforced")
	}
}
