// Package zerocracy реализует принципы Zerocracy: прозрачная политика баллов
// за результат (а не за время) и автоматическое распределение задач.
package zerocracy

import (
	"encoding/json"
	"fmt"
	"os"
)

// Policy — правила начисления баллов. Хранится в JSON и публикуется команде,
// чтобы каждый понимал, за что получает баллы.
type Policy struct {
	MRMerged           int            `json:"mr_merged"`             // влитый MR
	MRSmallBonus       int            `json:"mr_small_bonus"`        // бонус за компактный MR
	MRSmallLines       int            `json:"mr_small_lines"`        // порог «компактного» MR
	MRLargePenalty     int            `json:"mr_large_penalty"`      // штраф за огромный MR (отрицательное число)
	MRLargeLines       int            `json:"mr_large_lines"`        // порог «огромного» MR
	MRNoReviewPenalty  int            `json:"mr_no_review_penalty"`  // влит без ревью
	Review             int            `json:"review"`                // участие в ревью чужого MR
	ReviewComment      int            `json:"review_comment"`        // за каждый комментарий ревью
	ReviewCommentCap   int            `json:"review_comment_cap"`    // максимум комментариев в зачёт на MR
	IssueClosed        int            `json:"issue_closed"`          // закрытая задача без оценочной метки
	LabelPoints        map[string]int `json:"label_points"`          // баллы по меткам (size::S, bug …)
	CommitLinked       int            `json:"commit_linked"`         // коммит со ссылкой на задачу
	CommitLinkedPerDay int            `json:"commit_linked_per_day"` // максимум таких коммитов в зачёт за день
	OverduePenalty     int            `json:"overdue_penalty"`       // назначенная задача висит дольше SLA
	OverdueDays        int            `json:"overdue_days"`
}

// DefaultPolicy — политика по умолчанию (см. docs/zerocracy.md).
func DefaultPolicy() Policy {
	return Policy{
		MRMerged: 10, MRSmallBonus: 5, MRSmallLines: 200, MRLargePenalty: -5, MRLargeLines: 400,
		MRNoReviewPenalty: -3, Review: 3, ReviewComment: 1, ReviewCommentCap: 5, IssueClosed: 5,
		LabelPoints: map[string]int{
			"size::xs": 2, "size::s": 5, "size::m": 10, "size::l": 20, "size::xl": 30,
			"bug": 5, "critical": 15,
		},
		CommitLinked: 1, CommitLinkedPerDay: 5, OverduePenalty: -2, OverdueDays: 14,
	}
}

// LoadPolicy читает политику из файла; пустой путь — политика по умолчанию.
// Не указанные в файле поля берутся из политики по умолчанию.
func LoadPolicy(path string) (Policy, error) {
	p := DefaultPolicy()
	if path == "" {
		return p, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return p, fmt.Errorf("политика Zerocracy: %w", err)
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("политика Zerocracy: некорректный JSON: %w", err)
	}
	if p.MRSmallLines < 0 || p.MRLargeLines <= 0 || p.OverdueDays <= 0 {
		return p, fmt.Errorf("политика Zerocracy: пороги должны быть положительными")
	}
	return p, nil
}
