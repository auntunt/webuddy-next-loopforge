package repo

import (
	"encoding/json"
	"strings"
	"time"

	"agentflow/internal/domain"

	"gorm.io/gorm/clause"
)

type codeReviewRow struct {
	ID        string    `gorm:"column:id;type:varchar(128);primaryKey"`
	ProjectID string    `gorm:"column:project_id;type:varchar(128);index"`
	RunID     string    `gorm:"column:run_id;type:varchar(128);index"`
	Verdict   string    `gorm:"column:verdict;type:varchar(32);index"`
	Binding   string    `gorm:"column:binding;type:varchar(128)"`
	Payload   []byte    `gorm:"column:payload;type:longblob"`
	CreatedAt time.Time `gorm:"column:created_at;index"`
}

func (codeReviewRow) TableName() string { return "webuddy_code_reviews" }

type codeReviewFeedbackRow struct {
	ID            string    `gorm:"column:id;type:varchar(128);primaryKey"`
	ReviewID      string    `gorm:"column:review_id;type:varchar(128);index;uniqueIndex:idx_cr_fb_req,priority:1"`
	ProjectID     string    `gorm:"column:project_id;type:varchar(128);index"`
	Actor         string    `gorm:"column:actor;type:varchar(128);uniqueIndex:idx_cr_fb_req,priority:2"`
	RequestID     string    `gorm:"column:request_id;type:varchar(128);uniqueIndex:idx_cr_fb_req,priority:3"`
	Binding       string    `gorm:"column:binding;type:varchar(128)"`
	PayloadDigest string    `gorm:"column:payload_digest;type:varchar(128)"`
	Payload       []byte    `gorm:"column:payload;type:longblob"`
	CreatedAt     time.Time `gorm:"column:created_at;index"`
}

func (codeReviewFeedbackRow) TableName() string { return "webuddy_code_review_feedbacks" }

// CodeReviewRepo owns Webuddy code review records and human effectiveness feedbacks.
type CodeReviewRepo struct {
	*deps
}

func (r *CodeReviewRepo) CodeReviews(projectID string, limit int) []domain.CodeReviewRecord {
	if limit <= 0 {
		limit = 50
	}
	var rows []codeReviewRow
	q := r.DB.Order("created_at DESC").Limit(limit)
	if pid := strings.TrimSpace(projectID); pid != "" {
		q = q.Where("project_id = ?", pid)
	}
	if err := q.Find(&rows).Error; err != nil {
		return []domain.CodeReviewRecord{}
	}
	out := make([]domain.CodeReviewRecord, 0, len(rows))
	for _, row := range rows {
		var rec domain.CodeReviewRecord
		if err := json.Unmarshal(row.Payload, &rec); err == nil {
			out = append(out, rec)
		}
	}
	return out
}

func (r *CodeReviewRepo) CodeReview(id string) (domain.CodeReviewRecord, bool) {
	var row codeReviewRow
	if err := r.DB.Where("id = ?", strings.TrimSpace(id)).Take(&row).Error; err != nil {
		return domain.CodeReviewRecord{}, false
	}
	var rec domain.CodeReviewRecord
	if err := json.Unmarshal(row.Payload, &rec); err != nil {
		return domain.CodeReviewRecord{}, false
	}
	return rec, true
}

func (r *CodeReviewRepo) PutCodeReview(rec domain.CodeReviewRecord) error {
	payload, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	row := codeReviewRow{
		ID:        rec.ID,
		ProjectID: rec.ProjectID,
		RunID:     rec.RunID,
		Verdict:   rec.Verdict,
		Binding:   rec.Binding,
		Payload:   payload,
		CreatedAt: rec.CreatedAt,
	}
	return r.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"project_id", "run_id", "verdict", "binding", "payload", "created_at"}),
	}).Create(&row).Error
}

func (r *CodeReviewRepo) CodeReviewFeedbacks(reviewID string) []domain.CodeReviewFeedback {
	var rows []codeReviewFeedbackRow
	if err := r.DB.Where("review_id = ?", strings.TrimSpace(reviewID)).Order("created_at DESC").Find(&rows).Error; err != nil {
		return []domain.CodeReviewFeedback{}
	}
	return decodeFeedbackRows(rows)
}

func (r *CodeReviewRepo) CodeReviewFeedbacksByProject(projectID string) []domain.CodeReviewFeedback {
	var rows []codeReviewFeedbackRow
	q := r.DB.Order("created_at DESC")
	if pid := strings.TrimSpace(projectID); pid != "" {
		q = q.Where("project_id = ?", pid)
	}
	if err := q.Find(&rows).Error; err != nil {
		return []domain.CodeReviewFeedback{}
	}
	return decodeFeedbackRows(rows)
}

func (r *CodeReviewRepo) CodeReviewFeedbackByRequest(reviewID, actor, requestID string) (domain.CodeReviewFeedback, bool) {
	var row codeReviewFeedbackRow
	err := r.DB.Where("review_id = ? AND actor = ? AND request_id = ?",
		strings.TrimSpace(reviewID), strings.TrimSpace(actor), strings.TrimSpace(requestID),
	).Take(&row).Error
	if err != nil {
		return domain.CodeReviewFeedback{}, false
	}
	var fb domain.CodeReviewFeedback
	if err := json.Unmarshal(row.Payload, &fb); err != nil {
		return domain.CodeReviewFeedback{}, false
	}
	return fb, true
}

func (r *CodeReviewRepo) PutCodeReviewFeedback(fb domain.CodeReviewFeedback) error {
	payload, err := json.Marshal(fb)
	if err != nil {
		return err
	}
	row := codeReviewFeedbackRow{
		ID:            fb.ID,
		ReviewID:      fb.ReviewID,
		ProjectID:     fb.ProjectID,
		Actor:         fb.Actor,
		RequestID:     fb.RequestID,
		Binding:       fb.Binding,
		PayloadDigest: fb.PayloadDigest,
		Payload:       payload,
		CreatedAt:     fb.CreatedAt,
	}
	return r.DB.Create(&row).Error
}

func decodeFeedbackRows(rows []codeReviewFeedbackRow) []domain.CodeReviewFeedback {
	out := make([]domain.CodeReviewFeedback, 0, len(rows))
	for _, row := range rows {
		var fb domain.CodeReviewFeedback
		if err := json.Unmarshal(row.Payload, &fb); err == nil {
			out = append(out, fb)
		}
	}
	return out
}
