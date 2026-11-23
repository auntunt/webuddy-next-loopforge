-- Webuddy v2 Code Review & Human Effectiveness Feedback tables
CREATE TABLE IF NOT EXISTS webuddy_code_reviews (
  id VARCHAR(128) NOT NULL PRIMARY KEY,
  project_id VARCHAR(128) NOT NULL DEFAULT '',
  run_id VARCHAR(128) NOT NULL DEFAULT '',
  verdict VARCHAR(32) NOT NULL DEFAULT 'unknown',
  binding VARCHAR(128) NOT NULL DEFAULT '',
  payload LONGBLOB NOT NULL,
  created_at DATETIME(3) NOT NULL,
  INDEX idx_webuddy_cr_project (project_id),
  INDEX idx_webuddy_cr_run (run_id),
  INDEX idx_webuddy_cr_verdict (verdict),
  INDEX idx_webuddy_cr_created (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS webuddy_code_review_feedbacks (
  id VARCHAR(128) NOT NULL PRIMARY KEY,
  review_id VARCHAR(128) NOT NULL,
  project_id VARCHAR(128) NOT NULL DEFAULT '',
  actor VARCHAR(128) NOT NULL,
  request_id VARCHAR(128) NOT NULL,
  binding VARCHAR(128) NOT NULL,
  payload_digest VARCHAR(128) NOT NULL,
  payload LONGBLOB NOT NULL,
  created_at DATETIME(3) NOT NULL,
  UNIQUE KEY idx_cr_fb_req (review_id, actor, request_id),
  INDEX idx_webuddy_cr_fb_review (review_id),
  INDEX idx_webuddy_cr_fb_project (project_id),
  INDEX idx_webuddy_cr_fb_created (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
