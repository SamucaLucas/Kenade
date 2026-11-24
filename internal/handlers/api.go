package handlers

import (
	"Kenade/internal/auth"
	"Kenade/internal/models"
	"Kenade/internal/quiz"
	"Kenade/internal/ws"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

type Server struct {
	DB         *gorm.DB
	Hub        *ws.Hub
	timerMu    sync.Mutex
	roomTimers map[string]*time.Timer
}

func NewServer(db *gorm.DB, hub *ws.Hub) *Server {
	s := &Server{
		DB:         db,
		Hub:        hub,
		roomTimers: make(map[string]*time.Timer),
	}
	hub.OnPresenceChange = func(teamPIN string) {
		s.broadcastArenaState(teamPIN)
	}
	return s
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func (s *Server) currentUser(r *http.Request) (*models.User, error) {
	claims, err := auth.ExtractClaimsFromRequest(r)
	if err != nil {
		return nil, err
	}
	var user models.User
	if err := s.DB.First(&user, claims.UserID).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// isUserInTeam verifica se o usuário faz parte da equipe (ou é o criador/host da sala).
func (s *Server) isUserInTeam(team *models.ArenaTeam, userID uint) bool {
	if team.HostID == userID {
		return true
	}
	var count int64
	s.DB.Model(&models.ArenaTeamMember{}).
		Where("team_id = ? AND user_id = ?", team.ID, userID).
		Count(&count)
	return count > 0
}

// ============================================================================
// AUTENTICAÇÃO E PERFIS (1º Usuário = Admin automático, promove outros)
// ============================================================================

func (s *Server) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Método não permitido")
		return
	}

	var req struct {
		Name     string `json:"name"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Dados inválidos")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Username = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(req.Username)), "@")

	if req.Name == "" || req.Username == "" || len(req.Password) < 4 {
		writeError(w, http.StatusBadRequest, "Preencha nome, usuário e uma senha de pelo menos 4 caracteres")
		return
	}

	var existing models.User
	if err := s.DB.Where("username = ?", req.Username).First(&existing).Error; err == nil {
		writeError(w, http.StatusConflict, "Este @usuário já está em uso")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Erro ao processar senha")
		return
	}

	var userCount int64
	s.DB.Model(&models.User{}).Count(&userCount)
	role := models.RolePlayer
	if userCount == 0 {
		role = models.RoleAdmin
	}

	user := models.User{
		Name:         req.Name,
		Username:     req.Username,
		PasswordHash: hash,
		Role:         role,
	}

	if err := s.DB.Create(&user).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Erro ao criar conta")
		return
	}

	token, err := auth.GenerateToken(user.ID, user.Username, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Erro ao gerar sessão")
		return
	}
	auth.SetSessionCookie(w, token)

	stats, _ := quiz.GetUserSoloStats(s.DB, user.ID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"token": token,
		"user":  user,
		"stats": stats,
	})
}

func (s *Server) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Método não permitido")
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Dados inválidos")
		return
	}

	username := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(req.Username)), "@")
	var user models.User
	if err := s.DB.Where("username = ?", username).First(&user).Error; err != nil {
		writeError(w, http.StatusUnauthorized, "Usuário ou senha incorretos")
		return
	}

	if !auth.CheckPassword(req.Password, user.PasswordHash) {
		writeError(w, http.StatusUnauthorized, "Usuário ou senha incorretos")
		return
	}

	token, err := auth.GenerateToken(user.ID, user.Username, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Erro ao gerar sessão")
		return
	}
	auth.SetSessionCookie(w, token)

	stats, _ := quiz.GetUserSoloStats(s.DB, user.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user":  user,
		"stats": stats,
	})
}

func (s *Server) HandleLogout(w http.ResponseWriter, r *http.Request) {
	auth.ClearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) HandleMe(w http.ResponseWriter, r *http.Request) {
	user, err := s.currentUser(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Não autenticado")
		return
	}
	stats, _ := quiz.GetUserSoloStats(s.DB, user.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":  user,
		"stats": stats,
	})
}

// HandleUsers lista/pesquisa usuários pelo @username ou nome (para os comboboxes de equipe e promoção de admin).
func (s *Server) HandleUsers(w http.ResponseWriter, r *http.Request) {
	_, err := s.currentUser(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Não autenticado")
		return
	}

	q := strings.TrimPrefix(strings.TrimSpace(r.URL.Query().Get("q")), "@")
	query := s.DB.Order("username ASC")
	if q != "" {
		like := "%" + q + "%"
		query = query.Where("username ILIKE ? OR name ILIKE ?", like, like)
	}

	var users []models.User
	if err := query.Find(&users).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Erro ao listar usuários")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

// HandlePromoteUser permite que um Admin promova (ou altere o perfil de) outro usuário para Admin.
func (s *Server) HandlePromoteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Método não permitido")
		return
	}
	admin, err := s.currentUser(r)
	if err != nil || admin.Role != models.RoleAdmin {
		writeError(w, http.StatusForbidden, "Apenas administradores podem gerenciar permissões")
		return
	}

	var req struct {
		UserID uint   `json:"user_id"`
		Role   string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Dados inválidos")
		return
	}

	if req.Role != models.RoleAdmin && req.Role != models.RolePlayer {
		writeError(w, http.StatusBadRequest, "Perfil inválido")
		return
	}
	if req.UserID == admin.ID && req.Role != models.RoleAdmin {
		writeError(w, http.StatusBadRequest, "Você não pode remover sua própria permissão de admin")
		return
	}

	if err := s.DB.Model(&models.User{}).Where("id = ?", req.UserID).Update("role", req.Role).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Erro ao atualizar permissão")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ============================================================================
// BANCO COLABORATIVO DE QUESTÕES (Exclusivo Admins para Criar/Editar/Excluir)
// ============================================================================

func (s *Server) HandleQuestions(w http.ResponseWriter, r *http.Request) {
	user, err := s.currentUser(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Não autenticado")
		return
	}
	if user.Role != models.RoleAdmin {
		writeError(w, http.StatusForbidden, "Acesso restrito a administradores")
		return
	}

	switch r.Method {
	case http.MethodGet:
		category := strings.TrimSpace(r.URL.Query().Get("category"))
		creatorParam := strings.TrimSpace(r.URL.Query().Get("created_by"))
		search := strings.TrimSpace(r.URL.Query().Get("q"))

		query := s.DB.Preload("CreatedBy").Order("category ASC, id DESC")
		if category == models.CategoryGeneral || category == models.CategorySpecific {
			query = query.Where("category = ?", category)
		}
		if creatorParam == "me" {
			query = query.Where("created_by_id = ?", user.ID)
		} else if creatorID, err := strconv.ParseUint(creatorParam, 10, 64); err == nil && creatorID > 0 {
			query = query.Where("created_by_id = ?", creatorID)
		}
		if search != "" {
			like := "%" + search + "%"
			query = query.Where("statement ILIKE ? OR option_a ILIKE ? OR option_b ILIKE ? OR option_c ILIKE ? OR option_d ILIKE ?",
				like, like, like, like, like)
		}

		var questions []models.Question
		if err := query.Find(&questions).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "Erro ao buscar questões")
			return
		}

		var totalGeneral, totalSpecific int64
		s.DB.Model(&models.Question{}).Where("category = ?", models.CategoryGeneral).Count(&totalGeneral)
		s.DB.Model(&models.Question{}).Where("category = ?", models.CategorySpecific).Count(&totalSpecific)

		writeJSON(w, http.StatusOK, map[string]any{
			"questions":      questions,
			"total_general":  totalGeneral,
			"total_specific": totalSpecific,
			"total":          totalGeneral + totalSpecific,
		})

	case http.MethodPost:
		var req struct {
			Category       string `json:"category"`
			Statement      string `json:"statement"`
			OptionA        string `json:"option_a"`
			OptionB        string `json:"option_b"`
			OptionC        string `json:"option_c"`
			OptionD        string `json:"option_d"`
			CorrectOption  string `json:"correct_option"`
			Explanation    string `json:"explanation"`
			ReadingTimeSec int    `json:"reading_time_sec"`
			TimeLimitSec   int    `json:"time_limit_sec"`
			ForceCreate    bool   `json:"force_create"` // true quando o admin validou que a pergunta parecida segue lógica distinta
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "JSON inválido")
			return
		}

		q, err := validateAndBuildQuestion(req.Category, req.Statement, req.OptionA, req.OptionB, req.OptionC, req.OptionD, req.CorrectOption, req.Explanation, req.ReadingTimeSec, req.TimeLimitSec, user.ID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		// Verificação anti-duplicação (mesmo com 3+ admins salvando ao mesmo tempo!)
		exactMatch, similarMatches, err := quiz.CheckQuestionSimilarity(s.DB, q.Statement, q.OptionA, q.OptionB, q.OptionC, q.OptionD, 0)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Erro ao verificar similaridade da questão")
			return
		}
		// 1. Perguntas IDÊNTICAS nunca se cadastram (bloqueio estrito)
		if exactMatch != nil {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":       fmt.Sprintf("Bloqueado: Esta pergunta é idêntica à Questão #%d já cadastrada no banco e não pode ser duplicada.", exactMatch.Question.ID),
				"code":        "exact_duplicate",
				"exact_match": exactMatch,
			})
			return
		}
		// 2. Perguntas MUITO PARECIDAS exigem validação explícita do Admin confirmando que são questões distintas
		if len(similarMatches) > 0 && !req.ForceCreate {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":           "Encontramos pergunta(s) muito parecida(s) no banco. Compare abaixo e confirme se são realmente questões com lógicas distintas.",
				"code":            "similar_warning",
				"similar_matches": similarMatches,
			})
			return
		}

		if err := s.DB.Create(q).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "Erro ao salvar questão")
			return
		}
		s.DB.Preload("CreatedBy").First(q, q.ID)

		// Notifica em tempo real todos os outros admins que estão com a tela de questões aberta!
		s.Hub.Broadcast("__questions_admin__", "question_created", map[string]any{"question": q})

		writeJSON(w, http.StatusCreated, map[string]any{"question": q})

	default:
		writeError(w, http.StatusMethodNotAllowed, "Método não permitido")
	}
}

func (s *Server) HandleQuestionByID(w http.ResponseWriter, r *http.Request) {
	user, err := s.currentUser(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Não autenticado")
		return
	}
	if user.Role != models.RoleAdmin {
		writeError(w, http.StatusForbidden, "Apenas administradores podem alterar ou excluir questões")
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/api/questions/")
	qID, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || qID == 0 {
		writeError(w, http.StatusBadRequest, "ID de questão inválido")
		return
	}

	switch r.Method {
	case http.MethodPut:
		var existing models.Question
		if err := s.DB.First(&existing, qID).Error; err != nil {
			writeError(w, http.StatusNotFound, "Questão não encontrada")
			return
		}

		var req struct {
			Category       string `json:"category"`
			Statement      string `json:"statement"`
			OptionA        string `json:"option_a"`
			OptionB        string `json:"option_b"`
			OptionC        string `json:"option_c"`
			OptionD        string `json:"option_d"`
			CorrectOption  string `json:"correct_option"`
			Explanation    string `json:"explanation"`
			ReadingTimeSec int    `json:"reading_time_sec"`
			TimeLimitSec   int    `json:"time_limit_sec"`
			ForceCreate    bool   `json:"force_create"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "JSON inválido")
			return
		}

		validated, err := validateAndBuildQuestion(req.Category, req.Statement, req.OptionA, req.OptionB, req.OptionC, req.OptionD, req.CorrectOption, req.Explanation, req.ReadingTimeSec, req.TimeLimitSec, existing.CreatedByID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		exactMatch, similarMatches, err := quiz.CheckQuestionSimilarity(s.DB, validated.Statement, validated.OptionA, validated.OptionB, validated.OptionC, validated.OptionD, existing.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Erro ao verificar similaridade")
			return
		}
		if exactMatch != nil {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":       fmt.Sprintf("Bloqueado: Esta pergunta ficaria idêntica à Questão #%d já cadastrada.", exactMatch.Question.ID),
				"code":        "exact_duplicate",
				"exact_match": exactMatch,
			})
			return
		}
		if len(similarMatches) > 0 && !req.ForceCreate {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":           "Esta alteração deixa a pergunta muito parecida com outra já existente. Valide abaixo se são questões distintas.",
				"code":            "similar_warning",
				"similar_matches": similarMatches,
			})
			return
		}

		existing.Category = validated.Category
		existing.Statement = validated.Statement
		existing.StatementHash = validated.StatementHash
		existing.OptionA = validated.OptionA
		existing.OptionB = validated.OptionB
		existing.OptionC = validated.OptionC
		existing.OptionD = validated.OptionD
		existing.CorrectOption = validated.CorrectOption
		existing.Explanation = validated.Explanation
		existing.ReadingTimeSec = validated.ReadingTimeSec
		existing.TimeLimitSec = validated.TimeLimitSec

		if err := s.DB.Save(&existing).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "Erro ao atualizar questão")
			return
		}
		s.DB.Preload("CreatedBy").First(&existing, existing.ID)
		s.Hub.Broadcast("__questions_admin__", "question_updated", map[string]any{"question": existing})
		writeJSON(w, http.StatusOK, map[string]any{"question": existing})

	case http.MethodDelete:
		err := s.DB.Transaction(func(tx *gorm.DB) error {
			tx.Where("question_id = ?", qID).Delete(&models.UserQuestionProgress{})
			tx.Where("question_id = ?", qID).Delete(&models.ArenaTeamQuestionProgress{})
			tx.Where("question_id = ?", qID).Delete(&models.ArenaRoundAnswer{})
			return tx.Delete(&models.Question{}, qID).Error
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Erro ao excluir questão")
			return
		}
		s.Hub.Broadcast("__questions_admin__", "question_deleted", map[string]any{"id": qID})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		writeError(w, http.StatusMethodNotAllowed, "Método não permitido")
	}
}

func validateAndBuildQuestion(category, statement, optA, optB, optC, optD, correct, explanation string, readingTime, timeLimit int, creatorID uint) (*models.Question, error) {
	category = strings.ToLower(strings.TrimSpace(category))
	if category != models.CategoryGeneral && category != models.CategorySpecific {
		return nil, errors.New("selecione o tipo da pergunta: 1ª Etapa Geral ou 2ª Etapa Específica")
	}
	statement = strings.TrimSpace(statement)
	optA = strings.TrimSpace(optA)
	optB = strings.TrimSpace(optB)
	optC = strings.TrimSpace(optC)
	optD = strings.TrimSpace(optD)
	correct = strings.ToUpper(strings.TrimSpace(correct))
	explanation = strings.TrimSpace(explanation)

	if statement == "" || optA == "" || optB == "" || optC == "" || optD == "" {
		return nil, errors.New("preencha o enunciado e todas as 4 alternativas (A, B, C e D)")
	}
	if correct != "A" && correct != "B" && correct != "C" && correct != "D" {
		return nil, errors.New("marque a caixa de seleção da alternativa correta (A, B, C ou D)")
	}
	// Opções de tempo de leitura do enunciado: 5s, 10s, 15s (padrão), 20s ou 30s
	if readingTime != 5 && readingTime != 10 && readingTime != 15 && readingTime != 20 && readingTime != 30 {
		readingTime = models.DefaultReadingPhaseSeconds
	}
	// Opções de tempo para responder: 30s, 45s (padrão) ou 60s
	if timeLimit != 30 && timeLimit != 45 && timeLimit != 60 {
		timeLimit = 45
	}

	return &models.Question{
		Category:       category,
		Statement:      statement,
		StatementHash:  quiz.ComputeStatementHash(statement),
		OptionA:        optA,
		OptionB:        optB,
		OptionC:        optC,
		OptionD:        optD,
		CorrectOption:  correct,
		Explanation:    explanation,
		ReadingTimeSec: readingTime,
		TimeLimitSec:   timeLimit,
		CreatedByID:    creatorID,
	}, nil
}

// HandleCheckQuestionSimilarity verifica de forma assíncrona enquanto o Admin digita se a pergunta
// já existe (idêntica) ou se há perguntas muito parecidas sendo/já cadastradas pelos outros admins.
func (s *Server) HandleCheckQuestionSimilarity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Método não permitido")
		return
	}
	claims, err := auth.ExtractClaimsFromRequest(r)
	if err != nil || claims.Role != models.RoleAdmin {
		writeError(w, http.StatusForbidden, "Apenas administradores")
		return
	}

	var req struct {
		ExcludeID uint   `json:"exclude_id"`
		Statement string `json:"statement"`
		OptionA   string `json:"option_a"`
		OptionB   string `json:"option_b"`
		OptionC   string `json:"option_c"`
		OptionD   string `json:"option_d"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	exactMatch, similarMatches, err := quiz.CheckQuestionSimilarity(
		s.DB,
		req.Statement,
		req.OptionA,
		req.OptionB,
		req.OptionC,
		req.OptionD,
		req.ExcludeID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Erro ao verificar similaridade")
		return
	}

	status := "ok"
	if exactMatch != nil {
		status = "exact_duplicate"
	} else if len(similarMatches) > 0 {
		status = "similar_warning"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":          status,
		"exact_match":     exactMatch,
		"similar_matches": similarMatches,
	})
}

// HandleQuestionsWS mantém conexão WebSocket entre os 3+ admins na tela do Banco de Questões
// para atualizar a lista e revalidar duplicidades em tempo real quando alguém injeta uma questão.
func (s *Server) HandleQuestionsWS(w http.ResponseWriter, r *http.Request) {
	user, err := s.currentUser(r)
	if err != nil || user.Role != models.RoleAdmin {
		http.Error(w, "Acesso restrito a administradores", http.StatusForbidden)
		return
	}
	ws.ServeWS(s.Hub, w, r, "__questions_admin__", user.ID, "admin")
}

// ============================================================================
// MODO ESTUDO SOLO (Celular / Individual, Gerais -> Específicas, Ciclo Replay)
// ============================================================================

func (s *Server) HandleSoloNext(w http.ResponseWriter, r *http.Request) {
	// Usa os claims assinados do token diretamente para evitar um SELECT extra na tabela users
	claims, err := auth.ExtractClaimsFromRequest(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Não autenticado")
		return
	}

	res, err := quiz.SelectNextSoloQuestion(s.DB, claims.UserID)
	if err != nil {
		if errors.Is(err, quiz.ErrNoQuestionsInBank) {
			stats, _ := quiz.GetUserSoloStats(s.DB, claims.UserID)
			writeJSON(w, http.StatusOK, map[string]any{
				"empty": true,
				"stats": stats,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "Erro ao sortear questão: "+err.Error())
		return
	}

	q := *res.Question
	q.CorrectOption = ""
	q.Explanation = ""

	writeJSON(w, http.StatusOK, map[string]any{
		"empty":           false,
		"question":        q,
		"cycle_restarted": res.CycleRestarted,
		"active_phase":    res.ActivePhase,
		"stats":           res.Stats,
	})
}

func (s *Server) HandleSoloAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Método não permitido")
		return
	}

	claims, err := auth.ExtractClaimsFromRequest(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Não autenticado")
		return
	}

	var req struct {
		QuestionID     uint   `json:"question_id"`
		SelectedOption string `json:"selected_option"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Dados inválidos")
		return
	}
	req.SelectedOption = strings.ToUpper(strings.TrimSpace(req.SelectedOption))

	var question models.Question
	if err := s.DB.Select("id, correct_option, explanation").First(&question, req.QuestionID).Error; err != nil {
		writeError(w, http.StatusNotFound, "Questão não encontrada")
		return
	}

	isCorrect := req.SelectedOption == question.CorrectOption

	if err := recordUserQuestionProgress(s.DB, claims.UserID, question.ID, isCorrect, true); err != nil {
		writeError(w, http.StatusInternalServerError, "Erro ao registrar resposta")
		return
	}

	stats, _ := quiz.GetUserSoloStats(s.DB, claims.UserID)
	writeJSON(w, http.StatusOK, map[string]any{
		"is_correct":      isCorrect,
		"selected_option": req.SelectedOption,
		"correct_option":  question.CorrectOption,
		"explanation":     question.Explanation,
		"stats":           stats,
	})
}

// recordUserQuestionProgress executa um UPSERT atômico no PostgreSQL (ON CONFLICT) em vez de SELECT + INSERT/UPDATE!
func recordUserQuestionProgress(tx *gorm.DB, userID, questionID uint, isCorrect bool, updateSoloCycle bool) error {
	now := time.Now()
	incCorrect := 0
	incWrong := 0
	if isCorrect {
		incCorrect = 1
	} else {
		incWrong = 1
	}
	cycleVal := isCorrect && updateSoloCycle

	err := tx.Exec(`
		INSERT INTO user_question_progresses
			(user_id, question_id, mastered_ever, answered_correctly_in_cycle, times_correct, times_wrong, last_answered_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, question_id) DO UPDATE SET
			mastered_ever = user_question_progresses.mastered_ever OR EXCLUDED.mastered_ever,
			answered_correctly_in_cycle = CASE
				WHEN ? THEN (user_question_progresses.answered_correctly_in_cycle OR EXCLUDED.answered_correctly_in_cycle)
				ELSE user_question_progresses.answered_correctly_in_cycle
			END,
			times_correct = user_question_progresses.times_correct + EXCLUDED.times_correct,
			times_wrong = user_question_progresses.times_wrong + EXCLUDED.times_wrong,
			last_answered_at = EXCLUDED.last_answered_at
	`, userID, questionID, isCorrect, cycleVal, incCorrect, incWrong, now, updateSoloCycle).Error
	if err != nil {
		return err
	}

	return tx.Exec(`
		UPDATE users
		SET total_answered = total_answered + 1,
		    total_correct = total_correct + ?,
		    updated_at = ?
		WHERE id = ?
	`, incCorrect, now, userID).Error
}

// ============================================================================
// RANKING GLOBAL (JOGADORES INDIVIDUAIS + RANKING EXCLUSIVO DE EQUIPES)
// ============================================================================

type RankingEntry struct {
	Position      int     `json:"position"`
	UserID        uint    `json:"user_id"`
	Name          string  `json:"name"`
	Username      string  `json:"username"`
	Role          string  `json:"role"`
	UniqueCorrect int64   `json:"unique_correct"`
	TotalCorrect  int     `json:"total_correct"`
	TotalAnswered int     `json:"total_answered"`
	CycleCount    int     `json:"cycle_count"`
	Accuracy      float64 `json:"accuracy"`
}

type TeamRankingEntry struct {
	Position      int      `json:"position"`
	TeamID        uint     `json:"team_id"`
	Name          string   `json:"name"`
	PIN           string   `json:"pin"`
	HostName      string   `json:"host_name"`
	MemberCount   int      `json:"member_count"`
	MembersList   []string `json:"members_list"`
	UniqueCorrect int64    `json:"unique_correct"` // Questões únicas conquistadas pela equipe
	TotalCorrect  int64    `json:"total_correct"`  // Total de acertos acumulados pela equipe
	TotalScore    int64    `json:"total_score"`    // Soma de pontos da equipe
	CycleCount    int      `json:"cycle_count"`
}

func (s *Server) HandleRanking(w http.ResponseWriter, r *http.Request) {
	if _, err := auth.ExtractClaimsFromRequest(r); err != nil {
		writeError(w, http.StatusUnauthorized, "Não autenticado")
		return
	}

	var totalQuestions int64
	s.DB.Model(&models.Question{}).Count(&totalQuestions)

	// 1. Ranking Individual de Jogadores (1 query agregada)
	type rawRow struct {
		UserID        uint
		Name          string
		Username      string
		Role          string
		UniqueCorrect int64
		TotalCorrect  int
		TotalAnswered int
		CycleCount    int
	}

	var rows []rawRow
	err := s.DB.Table("users u").
		Select(`u.id as user_id, u.name, u.username, u.role, u.total_correct, u.total_answered, u.cycle_count,
			COUNT(CASE WHEN uqp.mastered_ever = true AND q.id IS NOT NULL THEN 1 END) as unique_correct`).
		Joins("LEFT JOIN user_question_progresses uqp ON uqp.user_id = u.id").
		Joins("LEFT JOIN questions q ON q.id = uqp.question_id").
		Group("u.id").
		Order("unique_correct DESC, u.total_correct DESC, u.cycle_count DESC, u.name ASC").
		Scan(&rows).Error

	if err != nil {
		writeError(w, http.StatusInternalServerError, "Erro ao carregar ranking global")
		return
	}

	ranking := make([]RankingEntry, 0, len(rows))
	for i, row := range rows {
		acc := 0.0
		if row.TotalAnswered > 0 {
			acc = float64(row.TotalCorrect) * 100.0 / float64(row.TotalAnswered)
		}
		ranking = append(ranking, RankingEntry{
			Position:      i + 1,
			UserID:        row.UserID,
			Name:          row.Name,
			Username:      row.Username,
			Role:          row.Role,
			UniqueCorrect: row.UniqueCorrect,
			TotalCorrect:  row.TotalCorrect,
			TotalAnswered: row.TotalAnswered,
			CycleCount:    row.CycleCount,
			Accuracy:      acc,
		})
	}

	// 2. Ranking Exclusivo de Equipes (Sem N+1 queries! Tudo agregado de uma vez)
	var teams []models.ArenaTeam
	s.DB.Preload("Host").Preload("Members.User").Find(&teams)

	type teamProgAgg struct {
		TeamID        uint
		UniqueCorrect int64
		TotalCorrect  int64
	}
	var progAggs []teamProgAgg
	s.DB.Raw(`
		SELECT
			atqp.team_id,
			COUNT(q.id) FILTER (WHERE atqp.mastered_ever = true OR atqp.answered_correctly_in_cycle = true OR atqp.times_correct > 0) AS unique_correct,
			COALESCE(SUM(atqp.times_correct), 0) AS total_correct
		FROM arena_team_question_progresses atqp
		JOIN questions q ON q.id = atqp.question_id
		GROUP BY atqp.team_id
	`).Scan(&progAggs)

	aggByTeam := make(map[uint]teamProgAgg, len(progAggs))
	for _, a := range progAggs {
		aggByTeam[a.TeamID] = a
	}

	teamRanking := make([]TeamRankingEntry, 0, len(teams))
	for _, t := range teams {
		agg := aggByTeam[t.ID]
		var totalScore int64
		membersList := make([]string, 0, len(t.Members))
		for _, m := range t.Members {
			totalScore += int64(m.ScoreInTeam)
			if m.User != nil {
				membersList = append(membersList, "@"+m.User.Username)
			}
		}

		hostName := ""
		if t.Host != nil {
			hostName = t.Host.Name
		}

		teamRanking = append(teamRanking, TeamRankingEntry{
			TeamID:        t.ID,
			Name:          t.Name,
			PIN:           t.PIN,
			HostName:      hostName,
			MemberCount:   len(t.Members),
			MembersList:   membersList,
			UniqueCorrect: agg.UniqueCorrect,
			TotalCorrect:  agg.TotalCorrect,
			TotalScore:    totalScore,
			CycleCount:    t.CycleCount,
		})
	}

	// Ordena Equipes por Acertos Únicos DESC, Total de Acertos DESC, Pontuação Total DESC
	for i := 0; i < len(teamRanking); i++ {
		for j := i + 1; j < len(teamRanking); j++ {
			a, b := teamRanking[i], teamRanking[j]
			swap := false
			if b.UniqueCorrect > a.UniqueCorrect {
				swap = true
			} else if b.UniqueCorrect == a.UniqueCorrect {
				if b.TotalCorrect > a.TotalCorrect {
					swap = true
				} else if b.TotalCorrect == a.TotalCorrect && b.TotalScore > a.TotalScore {
					swap = true
				}
			}
			if swap {
				teamRanking[i], teamRanking[j] = teamRanking[j], teamRanking[i]
			}
		}
	}
	for i := range teamRanking {
		teamRanking[i].Position = i + 1
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ranking":         ranking,
		"team_ranking":    teamRanking,
		"total_questions": totalQuestions,
	})
}

// ============================================================================
// MODO ARENA POR EQUIPE (VISIBILIDADE E ACESSO RESTRITOS AOS MEMBROS DA EQUIPE)
// ============================================================================

func (s *Server) HandleArenaTeams(w http.ResponseWriter, r *http.Request) {
	user, err := s.currentUser(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Não autenticado")
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Regra validada: A equipe SÓ aparece para quem está na equipe!
		memberSubQuery := s.DB.Model(&models.ArenaTeamMember{}).
			Select("team_id").
			Where("user_id = ?", user.ID)

		var teams []models.ArenaTeam
		s.DB.Preload("Host").Preload("Members.User").
			Where("host_id = ? OR id IN (?)", user.ID, memberSubQuery).
			Order("updated_at DESC").
			Find(&teams)

		// Busca contagens globais de questões e estatísticas de todas as equipes em lote (sem N+1 queries!)
		var qTotals struct {
			TotalQuestions int64
			TotalGeneral   int64
			TotalSpecific  int64
		}
		s.DB.Raw(`
			SELECT
				COUNT(id) AS total_questions,
				COUNT(id) FILTER (WHERE category = 'general') AS total_general,
				COUNT(id) FILTER (WHERE category = 'specific') AS total_specific
			FROM questions
		`).Scan(&qTotals)

		type teamCycleAgg struct {
			TeamID                uint
			MasteredInCycle       int64
			MasteredGeneralCycle  int64
			MasteredSpecificCycle int64
		}
		var cycleAggs []teamCycleAgg
		if len(teams) > 0 {
			teamIDs := make([]uint, 0, len(teams))
			for _, t := range teams {
				teamIDs = append(teamIDs, t.ID)
			}
			s.DB.Raw(`
				SELECT
					atqp.team_id,
					COUNT(q.id) FILTER (WHERE atqp.answered_correctly_in_cycle = true) AS mastered_in_cycle,
					COUNT(q.id) FILTER (WHERE atqp.answered_correctly_in_cycle = true AND q.category = 'general') AS mastered_general_cycle,
					COUNT(q.id) FILTER (WHERE atqp.answered_correctly_in_cycle = true AND q.category = 'specific') AS mastered_specific_cycle
				FROM arena_team_question_progresses atqp
				JOIN questions q ON q.id = atqp.question_id
				WHERE atqp.team_id IN ?
				GROUP BY atqp.team_id
			`, teamIDs).Scan(&cycleAggs)
		}
		aggMap := make(map[uint]teamCycleAgg, len(cycleAggs))
		for _, a := range cycleAggs {
			aggMap[a.TeamID] = a
		}

		type teamSummary struct {
			models.ArenaTeam
			IsHost bool               `json:"is_host"`
			Stats  quiz.ProgressStats `json:"stats"`
		}
		result := make([]teamSummary, 0, len(teams))
		for _, t := range teams {
			a := aggMap[t.ID]
			st := quiz.ProgressStats{
				TotalQuestions:        qTotals.TotalQuestions,
				TotalGeneral:          qTotals.TotalGeneral,
				TotalSpecific:         qTotals.TotalSpecific,
				MasteredInCycle:       a.MasteredInCycle,
				MasteredGeneralCycle:  a.MasteredGeneralCycle,
				MasteredSpecificCycle: a.MasteredSpecificCycle,
				PendingGeneral:        qTotals.TotalGeneral - a.MasteredGeneralCycle,
				PendingSpecific:       qTotals.TotalSpecific - a.MasteredSpecificCycle,
				CycleCount:            t.CycleCount,
			}
			result = append(result, teamSummary{
				ArenaTeam: t,
				IsHost:    t.HostID == user.ID,
				Stats:     st,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"teams": result})

	case http.MethodPost:
		var req struct {
			Name        string `json:"name"`
			MemberIDs   []uint `json:"member_ids"`
			NoTimeLimit bool   `json:"no_time_limit"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "Dados inválidos")
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		if req.Name == "" {
			writeError(w, http.StatusBadRequest, "Informe um nome para a Equipe / Sala")
			return
		}

		pin := s.generateUniquePIN()
		team := models.ArenaTeam{
			PIN:         pin,
			Name:        req.Name,
			HostID:      user.ID,
			RoundState:  models.RoundStateLobby,
			NoTimeLimit: req.NoTimeLimit,
		}

		err := s.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&team).Error; err != nil {
				return err
			}
			// Apenas o criador + os membros explicitamente pesquisados pelo @ e adicionados entram na equipe
			memberSet := map[uint]bool{user.ID: true}
			for _, id := range req.MemberIDs {
				if id > 0 {
					memberSet[id] = true
				}
			}
			for uid := range memberSet {
				member := models.ArenaTeamMember{
					TeamID: team.ID,
					UserID: uid,
				}
				if err := tx.Create(&member).Error; err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Erro ao criar sala/equipe Arena")
			return
		}

		state, _ := s.buildArenaTeamState(team.PIN, user.ID)
		writeJSON(w, http.StatusCreated, state)

	default:
		writeError(w, http.StatusMethodNotAllowed, "Método não permitido")
	}
}

func (s *Server) generateUniquePIN() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for {
		pin := fmt.Sprintf("%04d", r.Intn(9000)+1000)
		var count int64
		s.DB.Model(&models.ArenaTeam{}).Where("pin = ?", pin).Count(&count)
		if count == 0 {
			return pin
		}
	}
}

// HandleArenaTeamRoutes roteia /api/arena/teams/{pin}[/action]
func (s *Server) HandleArenaTeamRoutes(w http.ResponseWriter, r *http.Request) {
	user, err := s.currentUser(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Não autenticado")
		return
	}

	trimmed := strings.TrimPrefix(r.URL.Path, "/api/arena/teams/")
	parts := strings.Split(trimmed, "/")
	pin := strings.TrimSpace(parts[0])
	if pin == "" {
		writeError(w, http.StatusBadRequest, "PIN da sala não informado")
		return
	}

	var team models.ArenaTeam
	if err := s.DB.Where("pin = ?", pin).First(&team).Error; err != nil {
		writeError(w, http.StatusNotFound, "Sala não encontrada com o PIN "+pin)
		return
	}

	// Regra validada: Somente quem está na equipe pode acessar a equipe e participar (não adiciona sozinho)
	if !s.isUserInTeam(&team, user.ID) {
		writeError(w, http.StatusForbidden, "Acesso restrito: você não faz parte desta equipe. Peça ao criador da sala para adicionar seu @usuário.")
		return
	}

	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch action {
	case "":
		if r.Method == http.MethodGet {
			state, err := s.buildArenaTeamState(pin, user.ID)
			if err != nil {
				writeError(w, http.StatusNotFound, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, state)
			return
		}
		writeError(w, http.StatusMethodNotAllowed, "Método não permitido")

	case "join":
		// Valida a entrada de um membro autorizado da equipe no lobby mobile
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Método não permitido")
			return
		}
		s.broadcastArenaState(pin)
		state, _ := s.buildArenaTeamState(pin, user.ID)
		writeJSON(w, http.StatusOK, state)

	case "members":
		// Regra validada: Somente quem criou a sala (HostID) pode incluir ou remover membros!
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Método não permitido")
			return
		}
		if team.HostID != user.ID {
			writeError(w, http.StatusForbidden, "Apenas quem criou a sala pode incluir ou remover membros da equipe.")
			return
		}
		var req struct {
			UserID uint   `json:"user_id"`
			Action string `json:"action"` // "add" ou "remove"
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == 0 {
			writeError(w, http.StatusBadRequest, "Dados inválidos")
			return
		}
		if req.Action == "remove" {
			if req.UserID == team.HostID {
				writeError(w, http.StatusBadRequest, "O criador da sala não pode ser removido da própria equipe.")
				return
			}
			s.DB.Where("team_id = ? AND user_id = ?", team.ID, req.UserID).Delete(&models.ArenaTeamMember{})
		} else {
			var existing models.ArenaTeamMember
			if err := s.DB.Where("team_id = ? AND user_id = ?", team.ID, req.UserID).First(&existing).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				_ = s.DB.Create(&models.ArenaTeamMember{
					TeamID: team.ID,
					UserID: req.UserID,
				}).Error
			}
		}
		s.broadcastArenaState(pin)
		state, _ := s.buildArenaTeamState(pin, user.ID)
		writeJSON(w, http.StatusOK, state)

	case "next":
		// Apenas o Host que criou a sala inicia/sorteia a próxima questão para todos
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Método não permitido")
			return
		}
		if team.HostID != user.ID {
			writeError(w, http.StatusForbidden, "Apenas o criador (Host) da sala pode iniciar ou avançar as questões.")
			return
		}

		nextRes, err := quiz.SelectNextTeamQuestion(s.DB, team.ID)
		if err != nil {
			if errors.Is(err, quiz.ErrNoQuestionsInBank) {
				writeError(w, http.StatusBadRequest, "Nenhuma questão cadastrada no banco ainda! Cadastre questões primeiro.")
				return
			}
			writeError(w, http.StatusInternalServerError, "Erro ao sortear questão para a equipe")
			return
		}

		now := time.Now()
		team.CurrentQuestionID = &nextRes.Question.ID
		team.RoundNumber++
		team.RoundState = models.RoundStateQuestion
		team.RoundStartedAt = &now
		team.LastCycleRestarted = nextRes.CycleRestarted

		if err := s.DB.Save(&team).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "Erro ao iniciar rodada")
			return
		}

		// Soma o tempo de leitura configurado na questão + o tempo de responder da questão
		// Se NoTimeLimit estiver ativo na sala, NÃO agenda timer automático de encerramento (só encerra quando todos responderem ou o Host pular/revelar!)
		readingSec := nextRes.Question.ReadingTimeSec
		if readingSec <= 0 {
			readingSec = models.DefaultReadingPhaseSeconds
		}
		if team.NoTimeLimit {
			s.cancelRoundTimer(team.PIN)
		} else {
			totalDuration := readingSec + nextRes.Question.TimeLimitSec
			s.scheduleRoundTimer(team.PIN, team.RoundNumber, totalDuration)
		}
		s.broadcastArenaState(pin)
		state, _ := s.buildArenaTeamState(pin, user.ID)
		writeJSON(w, http.StatusOK, state)

	case "settings":
		// Permite ao Host ativar/desativar o modo "Sem Limite de Tempo para Responder" nas configurações da sala
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Método não permitido")
			return
		}
		if team.HostID != user.ID {
			writeError(w, http.StatusForbidden, "Apenas o criador (Host) da sala pode alterar as configurações de tempo.")
			return
		}
		var req struct {
			NoTimeLimit bool `json:"no_time_limit"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "Dados inválidos")
			return
		}
		team.NoTimeLimit = req.NoTimeLimit
		_ = s.DB.Model(&models.ArenaTeam{}).Where("id = ?", team.ID).Update("no_time_limit", req.NoTimeLimit).Error
		if req.NoTimeLimit {
			s.cancelRoundTimer(team.PIN)
		}
		s.broadcastArenaState(pin)
		state, _ := s.buildArenaTeamState(pin, user.ID)
		writeJSON(w, http.StatusOK, state)

	case "answer":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Método não permitido")
			return
		}
		var req struct {
			SelectedOption string `json:"selected_option"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "Dados inválidos")
			return
		}
		req.SelectedOption = strings.ToUpper(strings.TrimSpace(req.SelectedOption))
		if req.SelectedOption != "A" && req.SelectedOption != "B" && req.SelectedOption != "C" && req.SelectedOption != "D" {
			writeError(w, http.StatusBadRequest, "Alternativa inválida")
			return
		}

		if err := s.DB.Preload("CurrentQuestion").Preload("Members").Where("pin = ?", pin).First(&team).Error; err != nil {
			writeError(w, http.StatusNotFound, "Sala não encontrada")
			return
		}
		if team.RoundState != models.RoundStateQuestion || team.CurrentQuestion == nil {
			writeError(w, http.StatusBadRequest, "Nenhuma pergunta aberta para resposta neste momento")
			return
		}

		var member models.ArenaTeamMember
		if err := s.DB.Where("team_id = ? AND user_id = ?", team.ID, user.ID).First(&member).Error; err != nil {
			writeError(w, http.StatusForbidden, "Você não faz parte desta equipe")
			return
		}

		var existingAns models.ArenaRoundAnswer
		if err := s.DB.Where("team_id = ? AND round_number = ? AND user_id = ?", team.ID, team.RoundNumber, user.ID).First(&existingAns).Error; err == nil {
			writeError(w, http.StatusConflict, "Você já respondeu esta rodada")
			return
		}

		// Desconta o tempo configurado de leitura do enunciado no cálculo de velocidade
		readingSec := team.CurrentQuestion.ReadingTimeSec
		if readingSec <= 0 {
			readingSec = models.DefaultReadingPhaseSeconds
		}
		readingMs := int64(readingSec * 1000)
		elapsedMs := int64(0)
		if team.RoundStartedAt != nil {
			elapsedMs = time.Since(*team.RoundStartedAt).Milliseconds() - readingMs
			if elapsedMs < 0 {
				elapsedMs = 0
			}
		}
		limitMs := int64(team.CurrentQuestion.TimeLimitSec) * 1000
		if limitMs <= 0 {
			limitMs = 45000
		}

		isCorrect := req.SelectedOption == team.CurrentQuestion.CorrectOption
		points := 0
		if isCorrect {
			basePoints := 10
			speedBonus := 40
			if !team.NoTimeLimit {
				ratio := 1.0 - (float64(elapsedMs) / float64(limitMs))
				if ratio < 0 {
					ratio = 0
				}
				if ratio > 1 {
					ratio = 1
				}
				speedBonus = int(40.0*ratio + 0.5)
			}
			streakBonus := member.CurrentStreak * 5
			if streakBonus > 15 {
				streakBonus = 15
			}
			points = basePoints + speedBonus + streakBonus
		}

		ans := models.ArenaRoundAnswer{
			TeamID:         team.ID,
			RoundNumber:    team.RoundNumber,
			UserID:         user.ID,
			QuestionID:     team.CurrentQuestion.ID,
			SelectedOption: req.SelectedOption,
			IsCorrect:      isCorrect,
			PointsEarned:   points,
			ResponseTimeMs: elapsedMs,
		}
		if err := s.DB.Create(&ans).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "Erro ao salvar resposta")
			return
		}

		var answeredCount int64
		s.DB.Model(&models.ArenaRoundAnswer{}).
			Where("team_id = ? AND round_number = ?", team.ID, team.RoundNumber).
			Count(&answeredCount)

		var totalMembers int64
		s.DB.Model(&models.ArenaTeamMember{}).
			Where("team_id = ?", team.ID).
			Count(&totalMembers)

		if totalMembers > 0 && answeredCount >= totalMembers {
			_ = s.revealArenaRound(team.PIN, team.RoundNumber)
		} else {
			s.broadcastArenaState(pin)
		}

		state, _ := s.buildArenaTeamState(pin, user.ID)
		writeJSON(w, http.StatusOK, state)

	case "reveal":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Método não permitido")
			return
		}
		if team.HostID != user.ID {
			writeError(w, http.StatusForbidden, "Apenas o criador (Host) da sala pode revelar o gabarito manualmente.")
			return
		}
		_ = s.revealArenaRound(team.PIN, team.RoundNumber)
		state, _ := s.buildArenaTeamState(pin, user.ID)
		writeJSON(w, http.StatusOK, state)

	case "leaderboard":
		// Avança da tela de Gabarito/Gráfico para a tela de Ranking da Equipe (Top 5)
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Método não permitido")
			return
		}
		if team.HostID != user.ID {
			writeError(w, http.StatusForbidden, "Apenas o criador (Host) da sala pode avançar para o ranking.")
			return
		}
		s.cancelRoundTimer(pin)
		_ = s.DB.Model(&models.ArenaTeam{}).Where("id = ?", team.ID).Update("round_state", models.RoundStateLeaderboard).Error
		s.broadcastArenaState(pin)
		state, _ := s.buildArenaTeamState(pin, user.ID)
		writeJSON(w, http.StatusOK, state)

	case "reset":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Método não permitido")
			return
		}
		if team.HostID != user.ID {
			writeError(w, http.StatusForbidden, "Apenas o criador (Host) da sala pode zerar o progresso da equipe.")
			return
		}
		s.cancelRoundTimer(pin)
		_ = s.DB.Transaction(func(tx *gorm.DB) error {
			tx.Where("team_id = ?", team.ID).Delete(&models.ArenaTeamQuestionProgress{})
			tx.Where("team_id = ?", team.ID).Delete(&models.ArenaRoundAnswer{})
			tx.Model(&models.ArenaTeamMember{}).Where("team_id = ?", team.ID).Updates(map[string]any{
				"score_in_team":    0,
				"correct_in_team":  0,
				"answered_in_team": 0,
				"current_streak":   0,
			})
			return tx.Model(&models.ArenaTeam{}).Where("id = ?", team.ID).Updates(map[string]any{
				"cycle_count":          0,
				"round_number":         0,
				"round_state":          models.RoundStateLobby,
				"current_question_id":  nil,
				"last_cycle_restarted": false,
			}).Error
		})
		s.broadcastArenaState(pin)
		state, _ := s.buildArenaTeamState(pin, user.ID)
		writeJSON(w, http.StatusOK, state)

	default:
		writeError(w, http.StatusNotFound, "Ação inválida")
	}
}

func (s *Server) scheduleRoundTimer(pin string, roundNumber int, durationSec int) {
	if durationSec <= 0 {
		durationSec = 50
	}
	s.timerMu.Lock()
	if old, ok := s.roomTimers[pin]; ok {
		old.Stop()
	}
	s.roomTimers[pin] = time.AfterFunc(time.Duration(durationSec)*time.Second, func() {
		_ = s.revealArenaRound(pin, roundNumber)
	})
	s.timerMu.Unlock()
}

func (s *Server) cancelRoundTimer(pin string) {
	s.timerMu.Lock()
	if old, ok := s.roomTimers[pin]; ok {
		old.Stop()
		delete(s.roomTimers, pin)
	}
	s.timerMu.Unlock()
}

func (s *Server) revealArenaRound(pin string, roundNumber int) error {
	s.cancelRoundTimer(pin)

	err := s.DB.Transaction(func(tx *gorm.DB) error {
		var team models.ArenaTeam
		if err := tx.Where("pin = ?", pin).First(&team).Error; err != nil {
			return err
		}
		if team.RoundState != models.RoundStateQuestion || team.RoundNumber != roundNumber || team.CurrentQuestionID == nil {
			return nil
		}

		questionID := *team.CurrentQuestionID
		var answers []models.ArenaRoundAnswer
		tx.Where("team_id = ? AND round_number = ?", team.ID, roundNumber).Find(&answers)

		anyTeamMemberCorrect := false
		answeredUsers := make(map[uint]bool)

		for _, ans := range answers {
			answeredUsers[ans.UserID] = true
			if ans.IsCorrect {
				anyTeamMemberCorrect = true
			}

			var member models.ArenaTeamMember
			if err := tx.Where("team_id = ? AND user_id = ?", team.ID, ans.UserID).First(&member).Error; err == nil {
				member.AnsweredInTeam++
				if ans.IsCorrect {
					member.CorrectInTeam++
					member.ScoreInTeam += ans.PointsEarned
					member.CurrentStreak++
				} else {
					member.CurrentStreak = 0
				}
				_ = tx.Save(&member).Error
			}

			_ = recordUserQuestionProgress(tx, ans.UserID, questionID, ans.IsCorrect, false)
		}

		var allMembers []models.ArenaTeamMember
		tx.Where("team_id = ?", team.ID).Find(&allMembers)
		for _, m := range allMembers {
			if !answeredUsers[m.UserID] && m.CurrentStreak > 0 {
				tx.Model(&models.ArenaTeamMember{}).Where("id = ?", m.ID).Update("current_streak", 0)
			}
		}

		var teamProg models.ArenaTeamQuestionProgress
		err := tx.Where("team_id = ? AND question_id = ?", team.ID, questionID).First(&teamProg).Error
		now := time.Now()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			teamProg = models.ArenaTeamQuestionProgress{
				TeamID:                   team.ID,
				QuestionID:               questionID,
				MasteredEver:             anyTeamMemberCorrect,
				AnsweredCorrectlyInCycle: anyTeamMemberCorrect,
				LastAnsweredAt:           now,
			}
			if anyTeamMemberCorrect {
				teamProg.TimesCorrect = 1
			} else {
				teamProg.TimesWrong = 1
			}
			_ = tx.Create(&teamProg).Error
		} else if err == nil {
			teamProg.LastAnsweredAt = now
			if anyTeamMemberCorrect {
				teamProg.MasteredEver = true
				teamProg.AnsweredCorrectlyInCycle = true
				teamProg.TimesCorrect++
			} else {
				teamProg.TimesWrong++
			}
			_ = tx.Save(&teamProg).Error
		}

		return tx.Model(&models.ArenaTeam{}).Where("id = ?", team.ID).Update("round_state", models.RoundStateRevealed).Error
	})

	if err == nil {
		s.broadcastArenaState(pin)
	}
	return err
}

func (s *Server) buildArenaTeamState(pin string, viewerUserID uint) (map[string]any, error) {
	var team models.ArenaTeam
	if err := s.DB.Preload("Host").Preload("CurrentQuestion").Where("pin = ?", pin).First(&team).Error; err != nil {
		return nil, errors.New("sala Arena não encontrada")
	}

	var members []models.ArenaTeamMember
	s.DB.Preload("User").Where("team_id = ?", team.ID).Order("score_in_team DESC, correct_in_team DESC, id ASC").Find(&members)

	var roundAnswers []models.ArenaRoundAnswer
	if team.RoundNumber > 0 {
		s.DB.Where("team_id = ? AND round_number = ?", team.ID, team.RoundNumber).Find(&roundAnswers)
	}
	ansByUser := make(map[uint]models.ArenaRoundAnswer)
	for _, a := range roundAnswers {
		ansByUser[a.UserID] = a
	}

	onlineUsers := s.Hub.GetOnlineUsers(pin)

	type memberView struct {
		UserID             uint   `json:"user_id"`
		Name               string `json:"name"`
		Username           string `json:"username"`
		IsHost             bool   `json:"is_host"`
		ScoreInTeam        int    `json:"score_in_team"`
		CorrectInTeam      int    `json:"correct_in_team"`
		AnsweredInTeam     int    `json:"answered_in_team"`
		CurrentStreak      int    `json:"current_streak"`
		IsOnline           bool   `json:"is_online"`
		HasAnsweredCurrent bool   `json:"has_answered_current"`
		SelectedOption     string `json:"selected_option,omitempty"`
		IsCorrect          *bool  `json:"is_correct,omitempty"`
		PointsEarned       int    `json:"points_earned,omitempty"`
	}

	memberViews := make([]memberView, 0, len(members))
	for _, m := range members {
		if m.User == nil {
			continue
		}
		mv := memberView{
			UserID:         m.UserID,
			Name:           m.User.Name,
			Username:       m.User.Username,
			IsHost:         m.UserID == team.HostID,
			ScoreInTeam:    m.ScoreInTeam,
			CorrectInTeam:  m.CorrectInTeam,
			AnsweredInTeam: m.AnsweredInTeam,
			CurrentStreak:  m.CurrentStreak,
			IsOnline:       onlineUsers[m.UserID],
		}
		if ans, ok := ansByUser[m.UserID]; ok {
			mv.HasAnsweredCurrent = true
			if team.RoundState == models.RoundStateRevealed || team.RoundState == models.RoundStateLeaderboard || m.UserID == viewerUserID {
				mv.SelectedOption = ans.SelectedOption
			}
			if team.RoundState == models.RoundStateRevealed || team.RoundState == models.RoundStateLeaderboard {
				isCorr := ans.IsCorrect
				mv.IsCorrect = &isCorr
				mv.PointsEarned = ans.PointsEarned
			}
		}
		memberViews = append(memberViews, mv)
	}

	var questionView any = nil
	remainingSec := 0
	readingRemainingSec := 0
	readingPhaseTotal := models.DefaultReadingPhaseSeconds
	if team.CurrentQuestion != nil {
		q := *team.CurrentQuestion
		if q.ReadingTimeSec > 0 {
			readingPhaseTotal = q.ReadingTimeSec
		}
		if team.RoundState != models.RoundStateRevealed && team.RoundState != models.RoundStateLeaderboard {
			q.CorrectOption = ""
			q.Explanation = ""
		}
		questionView = q

		if team.RoundState == models.RoundStateQuestion && team.RoundStartedAt != nil {
			elapsed := int(time.Since(*team.RoundStartedAt).Seconds())
			if elapsed < readingPhaseTotal {
				readingRemainingSec = readingPhaseTotal - elapsed
				remainingSec = q.TimeLimitSec
			} else {
				readingRemainingSec = 0
				if team.NoTimeLimit {
					remainingSec = 0
				} else {
					answerElapsed := elapsed - readingPhaseTotal
					remainingSec = q.TimeLimitSec - answerElapsed
					if remainingSec < 0 {
						remainingSec = 0
					}
				}
			}
		}
	}

	stats, _ := quiz.GetTeamArenaStats(s.DB, team.ID)

	var myAnswer any = nil
	if viewerUserID > 0 {
		if ans, ok := ansByUser[viewerUserID]; ok {
			myData := map[string]any{
				"selected_option":  ans.SelectedOption,
				"response_time_ms": ans.ResponseTimeMs,
			}
			if team.RoundState == models.RoundStateRevealed || team.RoundState == models.RoundStateLeaderboard {
				myData["is_correct"] = ans.IsCorrect
				myData["points_earned"] = ans.PointsEarned
			}
			myAnswer = myData
		}
	}

	optionCounts := map[string]int{"A": 0, "B": 0, "C": 0, "D": 0}
	if team.RoundState == models.RoundStateRevealed || team.RoundState == models.RoundStateLeaderboard {
		for _, a := range roundAnswers {
			optionCounts[a.SelectedOption]++
		}
	}

	hostName := ""
	if team.Host != nil {
		hostName = team.Host.Name
	}

	return map[string]any{
		"team": map[string]any{
			"id":                    team.ID,
			"pin":                   team.PIN,
			"name":                  team.Name,
			"host_id":               team.HostID,
			"host_name":             hostName,
			"is_host":               viewerUserID == team.HostID,
			"cycle_count":           team.CycleCount,
			"round_number":          team.RoundNumber,
			"round_state":           team.RoundState,
			"no_time_limit":         team.NoTimeLimit,
			"last_cycle_restarted":  team.LastCycleRestarted,
			"reading_phase_total":   readingPhaseTotal,
			"reading_remaining_sec": readingRemainingSec,
			"remaining_sec":         remainingSec,
			"answered_count":        len(roundAnswers),
			"total_members":         len(memberViews),
		},
		"question":      questionView,
		"members":       memberViews,
		"stats":         stats,
		"my_answer":     myAnswer,
		"option_counts": optionCounts,
	}, nil
}

func (s *Server) broadcastArenaState(pin string) {
	state, err := s.buildArenaTeamState(pin, 0)
	if err != nil {
		return
	}
	s.Hub.Broadcast(pin, "arena_state", state)
}

func (s *Server) HandleArenaWS(w http.ResponseWriter, r *http.Request) {
	pin := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/ws/arena/"))
	if pin == "" {
		http.Error(w, "PIN obrigatório", http.StatusBadRequest)
		return
	}
	user, err := s.currentUser(r)
	if err != nil {
		http.Error(w, "Não autenticado", http.StatusUnauthorized)
		return
	}
	var team models.ArenaTeam
	if err := s.DB.Where("pin = ?", pin).First(&team).Error; err != nil {
		http.Error(w, "Sala não encontrada", http.StatusNotFound)
		return
	}
	if !s.isUserInTeam(&team, user.ID) {
		http.Error(w, "Acesso negado: você não faz parte desta equipe", http.StatusForbidden)
		return
	}

	role := r.URL.Query().Get("role")
	if role == "" {
		role = "player"
	}
	ws.ServeWS(s.Hub, w, r, pin, user.ID, role)
}
