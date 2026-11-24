package models

import (
	"time"

	"gorm.io/gorm"
)

const (
	RoleAdmin  = "admin"
	RolePlayer = "player"

	CategoryGeneral  = "general"  // Formação Geral (vem primeiro)
	CategorySpecific = "specific" // Componente Específico (vem depois das gerais)

	RoundStateLobby       = "lobby"
	RoundStateQuestion    = "question"
	RoundStateRevealed    = "revealed"
	RoundStateLeaderboard = "leaderboard"

	DefaultReadingPhaseSeconds = 15 // Tempo padrão (em segundos) exibindo apenas o enunciado para leitura
)

// User representa um jogador ou administrador cadastrado no Kenade.
type User struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	Username      string    `gorm:"size:64;uniqueIndex;not null" json:"username"`
	Name          string    `gorm:"size:120;not null" json:"name"`
	PasswordHash  string    `gorm:"not null" json:"-"`
	Role          string    `gorm:"size:20;not null;default:'player'" json:"role"`
	TotalCorrect  int       `gorm:"not null;default:0" json:"total_correct"`
	TotalAnswered int       `gorm:"not null;default:0" json:"total_answered"`
	CycleCount    int       `gorm:"not null;default:0" json:"cycle_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Question representa uma pergunta do banco único global do ENADE.
type Question struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	Category      string    `gorm:"size:20;not null;index;default:'general'" json:"category"` // "general" ou "specific"
	Statement     string    `gorm:"type:text;not null" json:"statement"`
	StatementHash string    `gorm:"size:64;index" json:"statement_hash"`
	OptionA       string    `gorm:"type:text;not null" json:"option_a"`
	OptionB       string    `gorm:"type:text;not null" json:"option_b"`
	OptionC       string    `gorm:"type:text;not null" json:"option_c"`
	OptionD       string    `gorm:"type:text;not null" json:"option_d"`
	CorrectOption  string    `gorm:"size:1;not null" json:"correct_option"` // "A", "B", "C" ou "D"
	Explanation    string    `gorm:"type:text" json:"explanation"`          // Opcional
	ReadingTimeSec int       `gorm:"not null;default:15" json:"reading_time_sec"`
	TimeLimitSec   int       `gorm:"not null;default:45" json:"time_limit_sec"`
	CreatedByID   uint      `gorm:"index" json:"created_by_id"`
	CreatedBy     *User     `gorm:"foreignKey:CreatedByID" json:"created_by,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// UserQuestionProgress controla o progresso individual no Modo Solo e os Acertos Únicos do Ranking Global.
type UserQuestionProgress struct {
	ID                       uint      `gorm:"primaryKey" json:"id"`
	UserID                   uint      `gorm:"uniqueIndex:idx_user_question;not null" json:"user_id"`
	QuestionID               uint      `gorm:"uniqueIndex:idx_user_question;not null" json:"question_id"`
	MasteredEver             bool      `gorm:"not null;default:false;index" json:"mastered_ever"`               // Usado no Ranking Global (Acertos Únicos)
	AnsweredCorrectlyInCycle bool      `gorm:"not null;default:false;index" json:"answered_correctly_in_cycle"` // Usado no ciclo anti-repetição do Modo Solo
	TimesCorrect             int       `gorm:"not null;default:0" json:"times_correct"`
	TimesWrong               int       `gorm:"not null;default:0" json:"times_wrong"`
	LastAnsweredAt           time.Time `json:"last_answered_at"`
}

// ArenaTeam representa uma Sala / Equipe criada para o Modo Arena (Monitor + Celulares),
// possuindo seu próprio ciclo de progresso e ranking exclusivo de equipes.
type ArenaTeam struct {
	ID                 uint              `gorm:"primaryKey" json:"id"`
	PIN                string            `gorm:"size:10;uniqueIndex;not null" json:"pin"`
	Name               string            `gorm:"size:120;not null" json:"name"`
	HostID             uint              `gorm:"not null;index" json:"host_id"`
	Host               *User             `gorm:"foreignKey:HostID" json:"host,omitempty"`
	CycleCount         int               `gorm:"not null;default:0" json:"cycle_count"`
	CurrentQuestionID  *uint             `gorm:"index" json:"current_question_id"`
	CurrentQuestion    *Question         `gorm:"foreignKey:CurrentQuestionID" json:"current_question,omitempty"`
	RoundNumber        int               `gorm:"not null;default:0" json:"round_number"`
	RoundState         string            `gorm:"size:20;not null;default:'lobby'" json:"round_state"` // lobby, question, revealed, leaderboard
	RoundStartedAt     *time.Time        `json:"round_started_at"`
	NoTimeLimit        bool              `gorm:"not null;default:false" json:"no_time_limit"` // Se true, mantém o tempo de leitura mas sem cronômetro limite para responder
	LastCycleRestarted bool              `gorm:"not null;default:false" json:"last_cycle_restarted"`
	Members            []ArenaTeamMember `gorm:"foreignKey:TeamID;constraint:OnDelete:CASCADE" json:"members,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

// ArenaTeamMember vincula um usuário cadastrado a uma Equipe/Sala do Modo Arena.
type ArenaTeamMember struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	TeamID         uint      `gorm:"uniqueIndex:idx_team_user;not null" json:"team_id"`
	UserID         uint      `gorm:"uniqueIndex:idx_team_user;not null" json:"user_id"`
	User           *User     `gorm:"foreignKey:UserID" json:"user,omitempty"`
	ScoreInTeam    int       `gorm:"not null;default:0" json:"score_in_team"`
	CorrectInTeam  int       `gorm:"not null;default:0" json:"correct_in_team"`
	AnsweredInTeam int       `gorm:"not null;default:0" json:"answered_in_team"`
	CurrentStreak  int       `gorm:"not null;default:0" json:"current_streak"`
	CreatedAt      time.Time `json:"created_at"`
}

// ArenaTeamQuestionProgress armazena o progresso exclusivo da Equipe/Sala no Modo Arena.
// Todos começam zerados na sala e seguem a ordem Gerais -> Específicas.
type ArenaTeamQuestionProgress struct {
	ID                       uint      `gorm:"primaryKey" json:"id"`
	TeamID                   uint      `gorm:"uniqueIndex:idx_team_question;not null" json:"team_id"`
	QuestionID               uint      `gorm:"uniqueIndex:idx_team_question;not null" json:"question_id"`
	MasteredEver             bool      `gorm:"not null;default:false;index" json:"mastered_ever"`
	AnsweredCorrectlyInCycle bool      `gorm:"not null;default:false;index" json:"answered_correctly_in_cycle"`
	TimesCorrect             int       `gorm:"not null;default:0" json:"times_correct"`
	TimesWrong               int       `gorm:"not null;default:0" json:"times_wrong"`
	LastAnsweredAt           time.Time `json:"last_answered_at"`
}

// ArenaRoundAnswer registra o voto de cada membro na rodada ativa da sala Arena.
type ArenaRoundAnswer struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	TeamID         uint      `gorm:"uniqueIndex:idx_round_answer;not null" json:"team_id"`
	RoundNumber    int       `gorm:"uniqueIndex:idx_round_answer;not null" json:"round_number"`
	UserID         uint      `gorm:"uniqueIndex:idx_round_answer;not null" json:"user_id"`
	User           *User     `gorm:"foreignKey:UserID" json:"user,omitempty"`
	QuestionID     uint      `gorm:"not null;index" json:"question_id"`
	SelectedOption string    `gorm:"size:1;not null" json:"selected_option"`
	IsCorrect      bool      `gorm:"not null" json:"is_correct"`
	PointsEarned   int       `gorm:"not null;default:0" json:"points_earned"`
	ResponseTimeMs int64     `gorm:"not null;default:0" json:"response_time_ms"`
	CreatedAt      time.Time `json:"created_at"`
}

// AutoMigrate executa a migração automática do GORM para todas as tabelas do sistema.
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&User{},
		&Question{},
		&UserQuestionProgress{},
		&ArenaTeam{},
		&ArenaTeamMember{},
		&ArenaTeamQuestionProgress{},
		&ArenaRoundAnswer{},
	)
}
