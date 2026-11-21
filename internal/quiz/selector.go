package quiz

import (
	"Kenade/internal/models"
	"errors"

	"gorm.io/gorm"
)

var ErrNoQuestionsInBank = errors.New("nenhuma questão cadastrada no banco ainda")

// ProgressStats resume a situação do banco de questões para um Usuário (Solo) ou Equipe (Arena).
type ProgressStats struct {
	TotalQuestions        int64 `json:"total_questions"`
	TotalGeneral          int64 `json:"total_general"`
	TotalSpecific         int64 `json:"total_specific"`
	MasteredInCycle       int64 `json:"mastered_in_cycle"`
	MasteredGeneralCycle  int64 `json:"mastered_general_cycle"`
	MasteredSpecificCycle int64 `json:"mastered_specific_cycle"`
	PendingGeneral        int64 `json:"pending_general"`
	PendingSpecific       int64 `json:"pending_specific"`
	UniqueMasteredEver    int64 `json:"unique_mastered_ever"`
	CycleCount            int   `json:"cycle_count"`
}

// NextQuestionResult encapsula a próxima questão selecionada e se houve reinício de ciclo (replay).
type NextQuestionResult struct {
	Question       *models.Question `json:"question"`
	CycleRestarted bool             `json:"cycle_restarted"`
	ActivePhase    string           `json:"active_phase"` // "general" ou "specific"
	Stats          ProgressStats    `json:"stats"`
}

// GetUserSoloStats calcula todas as estatísticas do usuário no banco de questões em 1 ÚNICA query SQL!
func GetUserSoloStats(db *gorm.DB, userID uint) (ProgressStats, error) {
	var stats ProgressStats

	err := db.Raw(`
		SELECT
			COUNT(q.id) AS total_questions,
			COUNT(q.id) FILTER (WHERE q.category = 'general') AS total_general,
			COUNT(q.id) FILTER (WHERE q.category = 'specific') AS total_specific,
			COUNT(q.id) FILTER (WHERE uqp.answered_correctly_in_cycle = true) AS mastered_in_cycle,
			COUNT(q.id) FILTER (WHERE uqp.answered_correctly_in_cycle = true AND q.category = 'general') AS mastered_general_cycle,
			COUNT(q.id) FILTER (WHERE uqp.answered_correctly_in_cycle = true AND q.category = 'specific') AS mastered_specific_cycle,
			COUNT(q.id) FILTER (WHERE uqp.mastered_ever = true) AS unique_mastered_ever,
			COALESCE((SELECT cycle_count FROM users WHERE id = ?), 0) AS cycle_count
		FROM questions q
		LEFT JOIN user_question_progresses uqp
			ON uqp.question_id = q.id AND uqp.user_id = ?
	`, userID, userID).Scan(&stats).Error

	if err != nil {
		return stats, err
	}

	stats.PendingGeneral = stats.TotalGeneral - stats.MasteredGeneralCycle
	if stats.PendingGeneral < 0 {
		stats.PendingGeneral = 0
	}
	stats.PendingSpecific = stats.TotalSpecific - stats.MasteredSpecificCycle
	if stats.PendingSpecific < 0 {
		stats.PendingSpecific = 0
	}

	return stats, nil
}

// SelectNextSoloQuestion escolhe a próxima pergunta para o Modo Solo com o mínimo de round-trips ao PostgreSQL:
// 1 query agregada de estatísticas + 1 query "ORDER BY RANDOM() LIMIT 1" que traz apenas 1 questão!
func SelectNextSoloQuestion(db *gorm.DB, userID uint) (*NextQuestionResult, error) {
	stats, err := GetUserSoloStats(db, userID)
	if err != nil {
		return nil, err
	}
	if stats.TotalQuestions == 0 {
		return nil, ErrNoQuestionsInBank
	}

	cycleRestarted := false

	// Se não há nem Gerais nem Específicas pendentes -> Usuário GABARITOU o banco! Reinicia ciclo.
	if stats.PendingGeneral == 0 && stats.PendingSpecific == 0 {
		cycleRestarted = true
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&models.UserQuestionProgress{}).
				Where("user_id = ?", userID).
				Update("answered_correctly_in_cycle", false).Error; err != nil {
				return err
			}
			return tx.Model(&models.User{}).
				Where("id = ?", userID).
				UpdateColumn("cycle_count", gorm.Expr("cycle_count + 1")).Error
		})
		if err != nil {
			return nil, err
		}

		// Atualiza estatísticas em memória sem precisar de outra viagem ao banco
		stats.MasteredInCycle = 0
		stats.MasteredGeneralCycle = 0
		stats.MasteredSpecificCycle = 0
		stats.PendingGeneral = stats.TotalGeneral
		stats.PendingSpecific = stats.TotalSpecific
		stats.CycleCount++
	}

	// Ordem estrita: 1º Gerais -> 2º Específicas
	targetCategory := models.CategoryGeneral
	if stats.PendingGeneral == 0 {
		targetCategory = models.CategorySpecific
	}

	var selected models.Question
	err = db.Raw(`
		SELECT q.*
		FROM questions q
		LEFT JOIN user_question_progresses uqp
			ON uqp.question_id = q.id AND uqp.user_id = ?
		WHERE q.category = ?
		  AND COALESCE(uqp.answered_correctly_in_cycle, false) = false
		ORDER BY RANDOM()
		LIMIT 1
	`, userID, targetCategory).Scan(&selected).Error

	if err != nil {
		return nil, err
	}
	if selected.ID == 0 {
		return nil, ErrNoQuestionsInBank
	}

	return &NextQuestionResult{
		Question:       &selected,
		CycleRestarted: cycleRestarted,
		ActivePhase:    targetCategory,
		Stats:          stats,
	}, nil
}

// GetTeamArenaStats calcula o progresso exclusivo da Equipe/Sala no Modo Arena em 1 ÚNICA query SQL!
func GetTeamArenaStats(db *gorm.DB, teamID uint) (ProgressStats, error) {
	var stats ProgressStats

	err := db.Raw(`
		SELECT
			COUNT(q.id) AS total_questions,
			COUNT(q.id) FILTER (WHERE q.category = 'general') AS total_general,
			COUNT(q.id) FILTER (WHERE q.category = 'specific') AS total_specific,
			COUNT(q.id) FILTER (WHERE atqp.answered_correctly_in_cycle = true) AS mastered_in_cycle,
			COUNT(q.id) FILTER (WHERE atqp.answered_correctly_in_cycle = true AND q.category = 'general') AS mastered_general_cycle,
			COUNT(q.id) FILTER (WHERE atqp.answered_correctly_in_cycle = true AND q.category = 'specific') AS mastered_specific_cycle,
			COUNT(q.id) FILTER (WHERE atqp.mastered_ever = true) AS unique_mastered_ever,
			COALESCE((SELECT cycle_count FROM arena_teams WHERE id = ?), 0) AS cycle_count
		FROM questions q
		LEFT JOIN arena_team_question_progresses atqp
			ON atqp.question_id = q.id AND atqp.team_id = ?
	`, teamID, teamID).Scan(&stats).Error

	if err != nil {
		return stats, err
	}

	stats.PendingGeneral = stats.TotalGeneral - stats.MasteredGeneralCycle
	if stats.PendingGeneral < 0 {
		stats.PendingGeneral = 0
	}
	stats.PendingSpecific = stats.TotalSpecific - stats.MasteredSpecificCycle
	if stats.PendingSpecific < 0 {
		stats.PendingSpecific = 0
	}

	return stats, nil
}

// SelectNextTeamQuestion escolhe a próxima pergunta para a Equipe/Sala no Modo Arena em apenas 2 queries SQL.
func SelectNextTeamQuestion(db *gorm.DB, teamID uint) (*NextQuestionResult, error) {
	stats, err := GetTeamArenaStats(db, teamID)
	if err != nil {
		return nil, err
	}
	if stats.TotalQuestions == 0 {
		return nil, ErrNoQuestionsInBank
	}

	cycleRestarted := false

	// Se a equipe gabaritou todas as Gerais e Específicas -> Reinicia o ciclo da equipe!
	if stats.PendingGeneral == 0 && stats.PendingSpecific == 0 {
		cycleRestarted = true
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&models.ArenaTeamQuestionProgress{}).
				Where("team_id = ?", teamID).
				Update("answered_correctly_in_cycle", false).Error; err != nil {
				return err
			}
			return tx.Model(&models.ArenaTeam{}).
				Where("id = ?", teamID).
				UpdateColumn("cycle_count", gorm.Expr("cycle_count + 1")).Error
		})
		if err != nil {
			return nil, err
		}

		stats.MasteredInCycle = 0
		stats.MasteredGeneralCycle = 0
		stats.MasteredSpecificCycle = 0
		stats.PendingGeneral = stats.TotalGeneral
		stats.PendingSpecific = stats.TotalSpecific
		stats.CycleCount++
	}

	targetCategory := models.CategoryGeneral
	if stats.PendingGeneral == 0 {
		targetCategory = models.CategorySpecific
	}

	var selected models.Question
	err = db.Raw(`
		SELECT q.*
		FROM questions q
		LEFT JOIN arena_team_question_progresses atqp
			ON atqp.question_id = q.id AND atqp.team_id = ?
		WHERE q.category = ?
		  AND COALESCE(atqp.answered_correctly_in_cycle, false) = false
		ORDER BY RANDOM()
		LIMIT 1
	`, teamID, targetCategory).Scan(&selected).Error

	if err != nil {
		return nil, err
	}
	if selected.ID == 0 {
		return nil, ErrNoQuestionsInBank
	}

	return &NextQuestionResult{
		Question:       &selected,
		CycleRestarted: cycleRestarted,
		ActivePhase:    targetCategory,
		Stats:          stats,
	}, nil
}
