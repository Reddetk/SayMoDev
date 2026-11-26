package service

import (
	"context"
	"time"

	"github.com/Reddetk/SayMoDev/progress-service/internal/core/valobj"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

func tariffView(snap *valobj.TariffSnapshot) *in.TariffSnapshotView {
	if snap == nil {
		return nil
	}
	return &in.TariffSnapshotView{
		WeekBitmap:  snap.WeekBitmap,
		Timezone:    snap.Timezone,
		ValidUntil:  snap.ValidUntil,
		ActivatedAt: snap.ActivatedAt.UTC(),
	}
}

func activeProgramView(p out.ActiveProgramRecord) *in.ActiveProgramView {
	return &in.ActiveProgramView{
		ProgramID:         p.ProgramID.String(),
		Status:            p.Status,
		ActivatedAt:       p.ActivatedAt.UTC(),
		CompletedAt:       utcPtr(p.CompletedAt),
		PausedAt:          utcPtr(p.PausedAt),
		CompletionPercent: p.CompletionPercent,
	}
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func grantedView(l out.LessonInfo) *in.GrantedLessonView {
	return &in.GrantedLessonView{LessonID: l.LessonID.String(), OrderPosition: l.OrderPosition}
}

func lessonInfoAt(prog out.ProgramInfo, position int) (out.LessonInfo, bool) {
	for _, l := range prog.Lessons {
		if l.OrderPosition == position {
			return l, true
		}
	}
	return out.LessonInfo{}, false
}

func firstLesson(prog out.ProgramInfo) (out.LessonInfo, bool) {
	if len(prog.Lessons) == 0 {
		return out.LessonInfo{}, false
	}
	best := prog.Lessons[0]
	for _, l := range prog.Lessons[1:] {
		if l.OrderPosition < best.OrderPosition {
			best = l
		}
	}
	return best, true
}

// lessonForCompleted is the next lesson after the given number of completed lessons.
func lessonForCompleted(prog out.ProgramInfo, completed int) (out.LessonInfo, bool) {
	return lessonInfoAt(prog, completed+1)
}

func accessibleView(accessible *out.AccessibleLessonRecord, available bool, next *time.Time, reason string) *in.AccessibleLessonView {
	if accessible == nil {
		return nil
	}
	// A granted row exists but the tariff forbids access (Rule 3): respond null.
	if !available && next == nil {
		return nil
	}
	v := &in.AccessibleLessonView{
		LessonID:   accessible.LessonID.String(),
		LessonType: accessible.LessonType,
	}
	if available {
		v.UnlockReason = "immediate"
		return v
	}
	v.NextUnlockDate = utcPtr(next)
	v.UnlockReason = reason
	return v
}

// answerViews maps records to the admin contract, replacing media payloads with presigned GET URLs.
func (s *AdminService) answerViews(ctx context.Context, recs []out.AnswerRecord, lessonTitles map[string]string) []in.AdminAnswerView {
	views := make([]in.AdminAnswerView, 0, len(recs))
	for _, r := range recs {
		payload := r.Payload
		if r.MediaType == string(valobj.MediaTypePhoto) || r.MediaType == string(valobj.MediaTypeVideo) {
			if key := objectKeyFromURL(payload); key != "" && s.storage != nil && s.storage.Configured() {
				if url, err := s.storage.PresignGet(ctx, key, s.presignGetTTL); err == nil {
					payload = url
				}
			}
		}
		views = append(views, in.AdminAnswerView{
			AnswerID:    r.AnswerID.String(),
			LessonID:    r.LessonID.String(),
			LessonTitle: lessonTitles[r.LessonID.String()],
			Payload:     payload,
			MediaType:   r.MediaType,
			SubmittedAt: r.SubmittedAt.UTC(),
		})
	}
	return views
}
