package usecase

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/port/out"
)

type fakeRepo struct {
	listCalls   atomic.Int32
	getCalls    atomic.Int32
	lessonCalls atomic.Int32
	delay       time.Duration
}

func (r *fakeRepo) List(context.Context, out.ProgramFilter) ([]out.ProgramSummary, error) {
	r.listCalls.Add(1)
	time.Sleep(r.delay)
	return []out.ProgramSummary{{ProgramID: "p1", Title: "Курс", Version: 1, TotalLessons: 1}}, nil
}

func (r *fakeRepo) Get(context.Context, string, int) (out.ProgramRecord, error) {
	r.getCalls.Add(1)
	return out.ProgramRecord{ProgramID: "p1", Title: "Курс", Version: 1}, nil
}

func (r *fakeRepo) GetLesson(context.Context, string, string) (out.LessonRecord, error) {
	r.lessonCalls.Add(1)
	time.Sleep(r.delay)
	return out.LessonRecord{
		LessonID:   "l1",
		Title:      "Урок",
		ContentURL: "https://storage.yandexcloud.net/saymo-programs/programs/p1/lessons/l1/v1.pdf",
	}, nil
}

type fakeCache struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newFakeCache() *fakeCache { return &fakeCache{data: map[string][]byte{}} }

func (c *fakeCache) Get(_ context.Context, key string, dst any) (bool, error) {
	c.mu.Lock()
	raw, ok := c.data[key]
	c.mu.Unlock()
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, dst)
}

func (c *fakeCache) Set(_ context.Context, key string, value any, _ time.Duration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.data[key] = raw
	c.mu.Unlock()
	return nil
}

func (c *fakeCache) Delete(_ context.Context, keys ...string) error {
	c.mu.Lock()
	for _, key := range keys {
		delete(c.data, key)
	}
	c.mu.Unlock()
	return nil
}

func (c *fakeCache) DeletePrefix(_ context.Context, prefix string) error {
	c.mu.Lock()
	for key := range c.data {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			delete(c.data, key)
		}
	}
	c.mu.Unlock()
	return nil
}

type countingPresigner struct{ calls atomic.Int32 }

func (p *countingPresigner) PresignGet(_ context.Context, contentURL string, _ time.Duration) (string, error) {
	p.calls.Add(1)
	return contentURL + "?X-Amz-Signature=test", nil
}

func TestListCachesInL1(t *testing.T) {
	repo := &fakeRepo{}
	query := NewProgramQuery(repo, nil, nil, nil)

	for i := 0; i < 3; i++ {
		if _, err := query.List(context.Background(), out.ProgramFilter{}); err != nil {
			t.Fatalf("list #%d: %v", i, err)
		}
	}
	if got := repo.listCalls.Load(); got != 1 {
		t.Fatalf("repo calls: want 1, got %d", got)
	}
}

func TestL2HitPopulatesL1(t *testing.T) {
	repo := &fakeRepo{}
	cache := newFakeCache()
	seed := []out.ProgramSummary{{ProgramID: "p1", Title: "Из кэша", Version: 1, TotalLessons: 1}}
	if err := cache.Set(context.Background(), "programs:all", seed, time.Hour); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	query := NewProgramQuery(repo, nil, cache, nil)
	for i := 0; i < 2; i++ {
		if _, err := query.List(context.Background(), out.ProgramFilter{}); err != nil {
			t.Fatalf("list: %v", err)
		}
	}
	if got := repo.listCalls.Load(); got != 0 {
		t.Fatalf("repo must not be called on L2 hit, got %d calls", got)
	}
	if _, ok := query.l1.Get("programs:all"); !ok {
		t.Fatal("L2 hit must populate L1")
	}
}

func TestGetLessonSingleflightAndPresignOnce(t *testing.T) {
	repo := &fakeRepo{delay: 20 * time.Millisecond}
	presigner := &countingPresigner{}
	query := NewProgramQuery(repo, presigner, newFakeCache(), nil)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := query.GetLesson(context.Background(), "p1", "l1"); err != nil {
				t.Errorf("get lesson: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := repo.lessonCalls.Load(); got != 1 {
		t.Fatalf("lesson repo calls: want 1, got %d", got)
	}
	if got := presigner.calls.Load(); got != 1 {
		t.Fatalf("presign calls: want 1, got %d", got)
	}
	if _, ok := query.l1.Get("lesson:p1:l1"); !ok {
		t.Fatal("lesson must be cached in L1")
	}
}

func TestListRejectsInvalidAphasiaType(t *testing.T) {
	repo := &fakeRepo{}
	query := NewProgramQuery(repo, nil, nil, nil)

	long := make([]rune, 51)
	for i := range long {
		long[i] = 'а'
	}
	if _, err := query.List(context.Background(), out.ProgramFilter{AphasiaType: string(long)}); err == nil {
		t.Fatal("51-rune aphasia_type must be rejected")
	}
	if got := repo.listCalls.Load(); got != 0 {
		t.Fatalf("invalid filter must not reach repository, got %d calls", got)
	}
}

func TestListFiltersCachedCatalogInMemory(t *testing.T) {
	cache := newFakeCache()
	seed := []out.ProgramSummary{
		{ProgramID: "p1", Difficulty: "easy", AphasiaType: "motor"},
		{ProgramID: "p2", Difficulty: "hard", AphasiaType: "sensory"},
	}
	if err := cache.Set(context.Background(), "programs:all", seed, time.Hour); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	repo := &fakeRepo{}
	query := NewProgramQuery(repo, nil, cache, nil)
	items, err := query.List(context.Background(), out.ProgramFilter{Difficulty: "easy", AphasiaType: "motor"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].ProgramID != "p1" {
		t.Fatalf("filtered result: %+v", items)
	}
	if repo.listCalls.Load() != 0 {
		t.Fatal("filter must not hit repository")
	}
}

func TestPresignedURLRegeneratedWhenLessThanFiveMinutesLeft(t *testing.T) {
	cache := newFakeCache()
	presigner := &countingPresigner{}
	query := NewProgramQuery(&fakeRepo{}, presigner, cache, nil)

	nearExpiry := presignedRecord{URL: "old-near-expiry", ExpiresAt: time.Now().Add(4 * time.Minute)}
	if err := cache.Set(context.Background(), "presigned_url:p1:l1", nearExpiry, time.Minute); err != nil {
		t.Fatalf("seed presign: %v", err)
	}

	lesson, err := query.GetLesson(context.Background(), "p1", "l1")
	if err != nil {
		t.Fatalf("get lesson: %v", err)
	}
	if presigner.calls.Load() != 1 {
		t.Fatalf("near-expiry URL must be regenerated, presign calls=%d", presigner.calls.Load())
	}
	if lesson.ContentURL == "old-near-expiry" {
		t.Fatal("stale presigned URL must not be served")
	}

	farExpiry := presignedRecord{URL: "cached-good", ExpiresAt: time.Now().Add(50 * time.Minute)}
	if err := cache.Set(context.Background(), "presigned_url:p1:l1", farExpiry, time.Minute); err != nil {
		t.Fatalf("seed presign: %v", err)
	}
	lesson, err = query.GetLesson(context.Background(), "p1", "l1")
	if err != nil {
		t.Fatalf("get lesson: %v", err)
	}
	if lesson.ContentURL != "cached-good" {
		t.Fatalf("fresh cached URL must be used, got %q", lesson.ContentURL)
	}
	if presigner.calls.Load() != 1 {
		t.Fatalf("fresh URL must not trigger presign, calls=%d", presigner.calls.Load())
	}
}

type fakePublisher struct {
	scopes []string
	err    error
}

func (p *fakePublisher) PublishInvalidation(_ context.Context, scope string) error {
	p.scopes = append(p.scopes, scope)
	return p.err
}

func TestInvalidationPublishesScopeAndClearsL1(t *testing.T) {
	query := NewProgramQuery(&fakeRepo{}, nil, nil, nil)
	publisher := &fakePublisher{}
	query.SetInvalidationPublisher(publisher)

	query.l1.Set("program:p1:v:1", out.ProgramRecord{ProgramID: "p1"}, time.Minute)
	query.l1.Set("lesson:p1:l1", out.LessonRecord{LessonID: "l1"}, time.Minute)
	query.l1.Set("programs:all", []out.ProgramSummary{{ProgramID: "p1"}}, time.Minute)

	if err := query.InvalidateProgram(context.Background(), "p1"); err != nil {
		t.Fatalf("invalidate program: %v", err)
	}
	for _, key := range []string{"program:p1:v:1", "lesson:p1:l1", "programs:all"} {
		if _, ok := query.l1.Get(key); ok {
			t.Fatalf("L1 key %q must be cleared", key)
		}
	}
	if len(publisher.scopes) != 1 || publisher.scopes[0] != out.InvalidationScopeProgram+"p1" {
		t.Fatalf("scopes: %v", publisher.scopes)
	}

	if err := query.InvalidateAll(context.Background()); err != nil {
		t.Fatalf("invalidate all: %v", err)
	}
	if len(publisher.scopes) != 2 || publisher.scopes[1] != out.InvalidationScopeAll {
		t.Fatalf("scopes: %v", publisher.scopes)
	}
}

func TestApplyInvalidationClearsOnlyLocalL1(t *testing.T) {
	query := NewProgramQuery(&fakeRepo{}, nil, nil, nil)
	query.l1.Set("program:p1:v:1", out.ProgramRecord{ProgramID: "p1"}, time.Minute)
	query.l1.Set("lesson:p1:l1", out.LessonRecord{LessonID: "l1"}, time.Minute)
	query.l1.Set("program:p2:v:1", out.ProgramRecord{ProgramID: "p2"}, time.Minute)
	query.l1.Set("programs:all", []out.ProgramSummary{{ProgramID: "p1"}}, time.Minute)

	query.ApplyInvalidation(out.InvalidationScopeProgram + "p1")

	for _, key := range []string{"program:p1:v:1", "lesson:p1:l1", "programs:all"} {
		if _, ok := query.l1.Get(key); ok {
			t.Fatalf("L1 key %q must be cleared for p1", key)
		}
	}
	if _, ok := query.l1.Get("program:p2:v:1"); !ok {
		t.Fatal("unrelated program cache must survive")
	}
}

func TestInvalidationPublishFailureDoesNotFailWrite(t *testing.T) {
	query := NewProgramQuery(&fakeRepo{}, nil, nil, nil)
	query.SetInvalidationPublisher(&fakePublisher{err: context.DeadlineExceeded})

	if err := query.InvalidateAll(context.Background()); err != nil {
		t.Fatalf("publish failure must not break write: %v", err)
	}
}
