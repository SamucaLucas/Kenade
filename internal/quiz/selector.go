package quiz

import (
	"Kenade/internal/models"
	"errors"
	"math/rand"
	"time"

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

// GetUserSoloStats calcula as estatísticas detalhadas do usuário no banco de questões.
func GetUserSoloStats(db *gorm.DB, userID uint) (ProgressStats, error) {
	var stats ProgressStats

	db.Model(&models.Question{}).Count(&stats.TotalQuestions)
	db.Model(&models.Question{}).Where("category = ?", models.CategoryGeneral).Count(&stats.TotalGeneral)
	db.Model(&models.Question{}).Where("category = ?", models.CategorySpecific).Count(&stats.TotalSpecific)

	// Acertos no ciclo atual (apenas de questões que ainda existem no banco)
	db.Table("user_question_progresses uqp").
		Joins("JOIN questions q ON q.id = uqp.question_id").
		Where("uqp.user_id = ? AND uqp.answered_correctly_in_cycle = ?", userID, true).
		Count(&stats.MasteredInCycle)

	db.Table("user_question_progresses uqp").
		Joins("JOIN questions q ON q.id = uqp.question_id").
		Where("uqp.user_id = ? AND uqp.answered_correctly_in_cycle = ? AND q.category = ?", userID, true, models.CategoryGeneral).
		Count(&stats.MasteredGeneralCycle)

	db.Table("user_question_progresses uqp").
		Joins("JOIN questions q ON q.id = uqp.question_id").
		Where("uqp.user_id = ? AND uqp.answered_correctly_in_cycle = ? AND q.category = ?", userID, true, models.CategorySpecific).
		Count(&stats.MasteredSpecificCycle)

	// Acertos únicos históricos (Ranking Global)
	db.Table("user_question_progresses uqp").
		Joins("JOIN questions q ON q.id = uqp.question_id").
		Where("uqp.user_id = ? AND uqp.mastered_ever = ?", userID, true).
		Count(&stats.UniqueMasteredEver)

	stats.PendingGeneral = stats.TotalGeneral - stats.MasteredGeneralCycle
	if stats.PendingGeneral < 0 {
		stats.PendingGeneral = 0
	}
	stats.PendingSpecific = stats.TotalSpecific - stats.MasteredSpecificCycle
	if stats.PendingSpecific < 0 {
		stats.PendingSpecific = 0
	}

	var user models.User
	if err := db.Select("cycle_count").First(&user, userID).Error; err == nil {
		stats.CycleCount = user.CycleCount
	}

	return stats, nil
}

// SelectNextSoloQuestion escolhe a próxima pergunta para o Modo Solo respeitando:
// 1. Não repetir perguntas já respondidas corretamente no ciclo atual.
// 2. Ordem estrita: primeiro todas as Gerais ("general"), depois as Específicas ("specific").
// 3. Se todas as perguntas foram acertadas, reinicia o ciclo (replay) automaticamente.
func SelectNextSoloQuestion(db *gorm.DB, userID uint) (*NextQuestionResult, error) {
	var totalQuestions int64
	if err := db.Model(&models.Question{}).Count(&totalQuestions).Error; err != nil {
		return nil, err
	}
	if totalQuestions == 0 {
		return nil, ErrNoQuestionsInBank
	}

	cycleRestarted := false

	// Função auxiliar que busca questões pendentes de uma categoria para o usuário
	findPendingByCategory := func(category string) ([]models.Question, error) {
		var questions []models.Question
		subQuery := db.Model(&models.UserQuestionProgress{}).
			Select("question_id").
			Where("user_id = ? AND answered_correctly_in_cycle = ?", userID, true)

		err := db.Where("category = ? AND id NOT IN (?)", category, subQuery).
			Find(&questions).Error
		return questions, err
	}

	// 1. Tenta buscar questões GERAIS pendentes
	candidates, err := findPendingByCategory(models.CategoryGeneral)
	if err != nil {
		return nil, err
	}
	activePhase := models.CategoryGeneral

	// 2. Se não há Gerais pendentes, busca ESPECÍFICAS pendentes
	if len(candidates) == 0 {
		candidates, err = findPendingByCategory(models.CategorySpecific)
		if err != nil {
			return nil, err
		}
		activePhase = models.CategorySpecific
	}

	// 3. Se não há nem Gerais nem Específicas pendentes -> Usuário GABARITOU o banco! Reinicia ciclo.
	if len(candidates) == 0 {
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

		// Busca novamente começando pelas Gerais
		candidates, err = findPendingByCategory(models.CategoryGeneral)
		if err != nil {
			return nil, err
		}
		activePhase = models.CategoryGeneral

		if len(candidates) == 0 {
			candidates, err = findPendingByCategory(models.CategorySpecific)
			if err != nil {
				return nil, err
			}
			activePhase = models.CategorySpecific
		}
	}

	if len(candidates) == 0 {
		return nil, ErrNoQuestionsInBank
	}

	selected := pickRandomQuestion(candidates)
	stats, _ := GetUserSoloStats(db, userID)

	return &NextQuestionResult{
		Question:       &selected,
		CycleRestarted: cycleRestarted,
		ActivePhase:    activePhase,
		Stats:          stats,
	}, nil
}

// GetTeamArenaStats calcula o progresso exclusivo da Equipe/Sala no Modo Arena.
func GetTeamArenaStats(db *gorm.DB, teamID uint) (ProgressStats, error) {
	var stats ProgressStats

	db.Model(&models.Question{}).Count(&stats.TotalQuestions)
	db.Model(&models.Question{}).Where("category = ?", models.CategoryGeneral).Count(&stats.TotalGeneral)
	db.Model(&models.Question{}).Where("category = ?", models.CategorySpecific).Count(&stats.TotalSpecific)

	db.Table("arena_team_question_progresses atqp").
		Joins("JOIN questions q ON q.id = atqp.question_id").
		Where("atqp.team_id = ? AND atqp.answered_correctly_in_cycle = ?", teamID, true).
		Count(&stats.MasteredInCycle)

	db.Table("arena_team_question_progresses atqp").
		Joins("JOIN questions q ON q.id = atqp.question_id").
		Where("atqp.team_id = ? AND atqp.answered_correctly_in_cycle = ? AND q.category = ?", teamID, true, models.CategoryGeneral).
		Count(&stats.MasteredGeneralCycle)

	db.Table("arena_team_question_progresses atqp").
		Joins("JOIN questions q ON q.id = atqp.question_id").
		Where("atqp.team_id = ? AND atqp.answered_correctly_in_cycle = ? AND q.category = ?", teamID, true, models.CategorySpecific).
		Count(&stats.MasteredSpecificCycle)

	stats.PendingGeneral = stats.TotalGeneral - stats.MasteredGeneralCycle
	if stats.PendingGeneral < 0 {
		stats.PendingGeneral = 0
	}
	stats.PendingSpecific = stats.TotalSpecific - stats.MasteredSpecificCycle
	if stats.PendingSpecific < 0 {
		stats.PendingSpecific = 0
	}

	var team models.ArenaTeam
	if err := db.Select("cycle_count").First(&team, teamID).Error; err == nil {
		stats.CycleCount = team.CycleCount
	}

	return stats, nil
}

// SelectNextTeamQuestion escolhe a próxima pergunta para a Equipe/Sala no Modo Arena,
// mantendo progresso próprio da sala (todos começam zerados na sala) e seguindo Gerais -> Específicas.
func SelectNextTeamQuestion(db *gorm.DB, teamID uint) (*NextQuestionResult, error) {
	var totalQuestions int64
	if err := db.Model(&models.Question{}).Count(&totalQuestions).Error; err != nil {
		return nil, err
	}
	if totalQuestions == 0 {
		return nil, ErrNoQuestionsInBank
	}

	cycleRestarted := false

	findPendingByCategory := func(category string) ([]models.Question, error) {
		var questions []models.Question
		subQuery := db.Model(&models.ArenaTeamQuestionProgress{}).
			Select("question_id").
			Where("team_id = ? AND answered_correctly_in_cycle = ?", teamID, true)

		err := db.Where("category = ? AND id NOT IN (?)", category, subQuery).
			Find(&questions).Error
		return questions, err
	}

	// 1. Busca Gerais pendentes para a equipe
	candidates, err := findPendingByCategory(models.CategoryGeneral)
	if err != nil {
		return nil, err
	}
	activePhase := models.CategoryGeneral

	// 2. Se esgotou Gerais na equipe, busca Específicas pendentes
	if len(candidates) == 0 {
		candidates, err = findPendingByCategory(models.CategorySpecific)
		if err != nil {
			return nil, err
		}
		activePhase = models.CategorySpecific
	}

	// 3. Se a equipe gabaritou todas as Gerais e Específicas -> Reinicia o ciclo da equipe!
	if len(candidates) == 0 {
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

		candidates, err = findPendingByCategory(models.CategoryGeneral)
		if err != nil {
			return nil, err
		}
		activePhase = models.CategoryGeneral

		if len(candidates) == 0 {
			candidates, err = findPendingByCategory(models.CategorySpecific)
			if err != nil {
				return nil, err
			}
			activePhase = models.CategorySpecific
		}
	}

	if len(candidates) == 0 {
		return nil, ErrNoQuestionsInBank
	}

	selected := pickRandomQuestion(candidates)
	stats, _ := GetTeamArenaStats(db, teamID)

	return &NextQuestionResult{
		Question:       &selected,
		CycleRestarted: cycleRestarted,
		ActivePhase:    activePhase,
		Stats:          stats,
	}, nil
}

func pickRandomQuestion(candidates []models.Question) models.Question {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return candidates[r.Intn(len(candidates))]
}
