package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mst8222/regto/internal/domain/shared"
	"github.com/mst8222/regto/internal/domain/story"
)

// StoryRepo 实现 story.Repository（PostgreSQL + pgx）。
type StoryRepo struct {
	pool *pgxpool.Pool
}

// NewStoryRepo 构造仓储；pool 不可为 nil。
func NewStoryRepo(pool *pgxpool.Pool) *StoryRepo {
	return &StoryRepo{pool: pool}
}

const storyColumns = `id, product_id, module_id, title, type, pri, estimate, status, stage,
	spec_version, closed_reason, reviewers, source_note, opened_by_id, opened_at,
	assigned_to_id, assigned_at, last_edited_by_id, last_edited_at, changed_by_id,
	closed_by_id, closed_at, deleted_at, created_at, updated_at`

// GetByID 加载需求聚合（含全部规格版本）。不存在或已软删返回 shared.ErrNotFound。
func (r *StoryRepo) GetByID(ctx context.Context, id int64) (*story.Story, error) {
	var (
		s          story.Story
		moduleID   *int64
		estimate   *float64
		reviewers  []byte
		openedAt   time.Time
		assignedAt *time.Time
		lastEdited *time.Time
		changedBy  *int64
		closedBy   *int64
		closedAt   *time.Time
		deletedAt  *time.Time
		createdAt  time.Time
		updatedAt  time.Time
		sourceNote *string
		closeRsn   *string
	)

	err := r.pool.QueryRow(ctx,
		`SELECT `+storyColumns+` FROM stories WHERE id = $1 AND deleted_at IS NULL`, id,
	).Scan(
		&s.ID, &s.ProductID, &moduleID, &s.Title, &s.Type, &s.Pri, &estimate, &s.Status, &s.Stage,
		&s.SpecVersion, &closeRsn, &reviewers, &sourceNote, &s.OpenedByID, &openedAt,
		&s.AssignedToID, &assignedAt, &s.LastEditedByID, &lastEdited, &changedBy,
		&closedBy, &closedAt, &deletedAt, &createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("story %d: %w", id, shared.ErrNotFound)
		}
		return nil, fmt.Errorf("查询 story 失败: %w", err)
	}

	s.ModuleID = moduleID
	s.Estimate = estimate
	s.OpenedAtMs = msOf(&openedAt)
	s.AssignedAtMs = msPtr(assignedAt)
	s.LastEditedAtMs = msPtr(lastEdited)
	s.ChangedByID = changedBy
	s.ClosedByID = closedBy
	s.ClosedAtMs = msPtr(closedAt)
	s.DeletedAtMs = msPtr(deletedAt)
	s.CreatedAtMs = msOf(&createdAt)
	s.UpdatedAtMs = msOf(&updatedAt)
	s.LoadedAtMs = s.UpdatedAtMs // 乐观锁基准（DEC-07）
	if closeRsn != nil {
		s.ClosedReason = *closeRsn
	}
	if sourceNote != nil {
		s.SourceNote = *sourceNote
	}
	if len(reviewers) > 0 {
		var ids []int64
		if err := json.Unmarshal(reviewers, &ids); err != nil {
			return nil, fmt.Errorf("解析 reviewers JSON 失败: %w", err)
		}
		s.Reviewers = ids
	}

	specs, err := r.loadSpecs(ctx, id)
	if err != nil {
		return nil, err
	}
	s.Specs = specs
	return &s, nil
}

func (r *StoryRepo) loadSpecs(ctx context.Context, storyID int64) ([]story.Spec, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, story_id, version, title, spec, verify, pri, estimate, created_by_id, created_at
		   FROM story_specs WHERE story_id = $1 ORDER BY version`, storyID)
	if err != nil {
		return nil, fmt.Errorf("查询 story_specs 失败: %w", err)
	}
	defer rows.Close()

	var out []story.Spec
	for rows.Next() {
		var (
			sp       story.Spec
			specText *string
			verify   *string
			estimate *float64
			created  time.Time
		)
		if err := rows.Scan(&sp.ID, &sp.StoryID, &sp.Version, &sp.Title, &specText, &verify,
			&sp.Pri, &estimate, &sp.CreatedByID, &created); err != nil {
			return nil, fmt.Errorf("扫描 story_specs 失败: %w", err)
		}
		if specText != nil {
			sp.Spec = *specText
		}
		if verify != nil {
			sp.Verify = *verify
		}
		sp.Estimate = estimate
		sp.CreatedAt = msOf(&created)
		out = append(out, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 story_specs 失败: %w", err)
	}
	return out, nil
}

// Save 在同一事务内保存聚合、规格版本与领域事件（activities + activity_changes）。
// 乐观锁：UPDATE ... WHERE id=$1 AND updated_at = LoadedAtMs，影响行数为 0 → shared.ErrConflict（DEC-07）。
func (r *StoryRepo) Save(ctx context.Context, s *story.Story, events []story.Event) error {
	if s == nil {
		return errors.New("story is nil")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := r.saveStory(ctx, tx, s); err != nil {
		return err
	}
	if err := r.saveSpecs(ctx, tx, s); err != nil {
		return err
	}
	if err := r.saveEvents(ctx, tx, s, events); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}

// saveStory 写主表：新建走 INSERT，已有走乐观锁 UPDATE。
func (r *StoryRepo) saveStory(ctx context.Context, tx pgx.Tx, s *story.Story) error {
	reviewers, err := json.Marshal(s.Reviewers)
	if err != nil {
		return fmt.Errorf("序列化 reviewers 失败: %w", err)
	}
	now := time.Now().UTC()

	if s.LoadedAtMs == 0 {
		createdAt, updatedAt := now, now
		if s.CreatedAtMs != 0 {
			createdAt = ms(s.CreatedAtMs)
		}
		if s.UpdatedAtMs != 0 {
			updatedAt = ms(s.UpdatedAtMs)
		}
		var newUpdated time.Time
		err := tx.QueryRow(ctx, `
			INSERT INTO stories (product_id, module_id, title, type, pri, estimate, status, stage,
				spec_version, closed_reason, reviewers, source_note, opened_by_id, opened_at,
				assigned_to_id, assigned_at, last_edited_by_id, last_edited_at, changed_by_id,
				closed_by_id, closed_at, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)
			RETURNING id, updated_at`,
			s.ProductID, s.ModuleID, s.Title, s.Type, s.Pri, s.Estimate, s.Status, s.Stage,
			s.SpecVersion, nullString(s.ClosedReason), reviewers, nullString(s.SourceNote),
			s.OpenedByID, ms(s.OpenedAtMs), s.AssignedToID, timeOrNil(s.AssignedAtMs),
			s.LastEditedByID, timeOrNil(s.LastEditedAtMs), s.ChangedByID,
			s.ClosedByID, timeOrNil(s.ClosedAtMs), createdAt, updatedAt,
		).Scan(&s.ID, &newUpdated)
		if err != nil {
			return fmt.Errorf("插入 stories 失败: %w", err)
		}
		s.UpdatedAtMs = msOf(&newUpdated)
		s.LoadedAtMs = s.UpdatedAtMs
		for i := range s.Specs {
			s.Specs[i].StoryID = s.ID
		}
		return nil
	}

	ct, err := tx.Exec(ctx, `
		UPDATE stories SET product_id=$2, module_id=$3, title=$4, type=$5, pri=$6, estimate=$7,
			status=$8, stage=$9, spec_version=$10, closed_reason=$11, reviewers=$12, source_note=$13,
			assigned_to_id=$14, assigned_at=$15, last_edited_by_id=$16, last_edited_at=$17,
			changed_by_id=$18, closed_by_id=$19, closed_at=$20, updated_at=$21
		 WHERE id=$1 AND updated_at=$22 AND deleted_at IS NULL`,
		s.ID, s.ProductID, s.ModuleID, s.Title, s.Type, s.Pri, s.Estimate, s.Status, s.Stage,
		s.SpecVersion, nullString(s.ClosedReason), reviewers, nullString(s.SourceNote),
		s.AssignedToID, timeOrNil(s.AssignedAtMs), s.LastEditedByID, timeOrNil(s.LastEditedAtMs),
		s.ChangedByID, s.ClosedByID, timeOrNil(s.ClosedAtMs), ms(s.UpdatedAtMs), ms(s.LoadedAtMs),
	)
	if err != nil {
		return fmt.Errorf("更新 stories 失败: %w", err)
	}
	if ct.RowsAffected() == 0 {
		// 要么并发冲突（updated_at 已被他人改写），要么记录已被软删/删除。
		return fmt.Errorf("story %d: %w", s.ID, shared.ErrConflict)
	}
	s.LoadedAtMs = s.UpdatedAtMs
	return nil
}

// saveSpecs 写入规格版本：历史版本不可变，故用 ON CONFLICT DO NOTHING 幂等落库。
func (r *StoryRepo) saveSpecs(ctx context.Context, tx pgx.Tx, s *story.Story) error {
	for _, sp := range s.Specs {
		_, err := tx.Exec(ctx, `
			INSERT INTO story_specs (story_id, version, title, spec, verify, pri, estimate, created_by_id, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (story_id, version) DO NOTHING`,
			sp.StoryID, sp.Version, sp.Title, nullString(sp.Spec), nullString(sp.Verify),
			sp.Pri, sp.Estimate, sp.CreatedByID, ms(sp.CreatedAt))
		if err != nil {
			return fmt.Errorf("插入 story_specs(version=%d) 失败: %w", sp.Version, err)
		}
	}
	return nil
}

// saveEvents 把领域事件落为审计流水（activities + activity_changes）。
// 这是「工作即留痕」的物理落点：actor_type 区分 human/ai/system（AGENTS.md §3.1）。
func (r *StoryRepo) saveEvents(ctx context.Context, tx pgx.Tx, s *story.Story, events []story.Event) error {
	for _, e := range events {
		extra, err := json.Marshal(map[string]any{
			"spec_ref": e.SpecRef,
			"changes":  len(e.Changes),
		})
		if err != nil {
			return fmt.Errorf("序列化 extra 失败: %w", err)
		}
		actorType := e.Actor.Type
		if actorType == "" {
			actorType = "system"
		}
		var activityID int64
		err = tx.QueryRow(ctx, `
			INSERT INTO activities (object_type, object_id, product_id, actor_type, actor_id,
				action, comment, extra, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
			e.ObjectType, s.ID, s.ProductID, actorType, fmt.Sprintf("%d", e.Actor.ID),
			string(e.Action), nullString(e.Comment), extra, ms(e.OccurredAtMs),
		).Scan(&activityID)
		if err != nil {
			return fmt.Errorf("插入 activities 失败: %w", err)
		}
		for _, c := range e.Changes {
			if _, err := tx.Exec(ctx, `
				INSERT INTO activity_changes (activity_id, field, old_value, new_value)
				VALUES ($1,$2,$3,$4)`, activityID, c.Field, nullString(c.From), nullString(c.To)); err != nil {
				return fmt.Errorf("插入 activity_changes 失败: %w", err)
			}
		}
		// 评审单据与状态转移同事务：转移失败则评审单据一并回滚（DEC-09）。
		// 旧实现在独立连接先写评审再转移，导致「非法转移也会留下脏评审记录」。
		if e.Review != nil {
			if _, err := tx.Exec(ctx, `
				INSERT INTO story_reviews (story_id, version, reviewer_id, result, comment, reviewed_at)
				VALUES ($1,$2,$3,$4,$5,$6)`,
				e.Review.StoryID, e.Review.Version, e.Review.ReviewerID, string(e.Review.Result),
				nullString(e.Review.Comment), ms(e.Review.ReviewedAtMs)); err != nil {
				return fmt.Errorf("插入 story_reviews 失败: %w", err)
			}
		}
	}
	return nil
}

// nullString 把空串写为 NULL（禅道语义：空串与未填等价）。
func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
