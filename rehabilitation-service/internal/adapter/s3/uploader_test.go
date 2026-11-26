package s3

import (
	"testing"
	"time"
)

func TestBreakerOpensAfterFiveFailures(t *testing.T) {
	now := time.Now()
	b := breaker{now: func() time.Time { return now }}

	for i := 0; i < 5; i++ {
		if !b.allow() {
			t.Fatalf("breaker must be closed before failure %d", i+1)
		}
		b.failure()
	}
	if b.allow() {
		t.Fatal("breaker must be open after 5 failures")
	}

	now = now.Add(31 * time.Second)
	if !b.allow() {
		t.Fatal("breaker must half-open after 30s")
	}
}

func TestBreakerSuccessResetsFailures(t *testing.T) {
	b := breaker{now: time.Now}
	for i := 0; i < 4; i++ {
		b.failure()
	}
	b.success()
	for i := 0; i < 4; i++ {
		if !b.allow() {
			t.Fatal("breaker must remain closed after reset")
		}
		b.failure()
	}
	if !b.allow() {
		t.Fatal("4 failures after reset must not open the breaker")
	}
}

func TestObjectURL(t *testing.T) {
	u := NewUploader(nil, "saymo-programs", "https://storage.yandexcloud.net/")
	got, err := u.ObjectURL("programs/p1/lessons/l1/v1.pdf")
	if err != nil {
		t.Fatalf("object url: %v", err)
	}
	want := "https://storage.yandexcloud.net/saymo-programs/programs/p1/lessons/l1/v1.pdf"
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}
