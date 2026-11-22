package main

import (
	"Kenade/internal/auth"
	"Kenade/internal/handlers"
	"Kenade/internal/models"
	"Kenade/internal/ws"
	"bufio"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func loadDotEnv(filename string) {
	file, err := os.Open(filename)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, `"'`)
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("Erro ao ler arquivo %s: %v", filename, err)
	}
}

func getDatabaseDSN() string {
	for _, key := range []string{"DATABASE_URL", "POSTGRES_URL", "DB_DSN"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

func isAuthenticatedRequest(r *http.Request) bool {
	_, err := auth.ExtractClaimsFromRequest(r)
	return err == nil
}

// serveAdminPage garante que apenas usuários com perfil Admin no banco possam acessar páginas administrativas.
func serveAdminPage(db *gorm.DB, filePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, err := auth.ExtractClaimsFromRequest(r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		var user models.User
		if err := db.First(&user, claims.UserID).Error; err != nil || user.Role != models.RoleAdmin {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		http.ServeFile(w, r, filePath)
	}
}
func serveProtectedPage(filePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAuthenticatedRequest(r) {
			target := "/login"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		http.ServeFile(w, r, filePath)
	}
}

func main() {
	loadDotEnv(".env")

	dsn := getDatabaseDSN()
	if dsn == "" {
		log.Fatal("ERRO: Variável de ambiente DATABASE_URL não definida. Configure DATABASE_URL no arquivo .env ou no painel do Render com a URL do seu PostgreSQL online.")
	}

	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN:                  dsn,
		PreferSimpleProtocol: true, // Evita round-trip extra de Parse/Bind em cada query em bancos PostgreSQL na nuvem
	}), &gorm.Config{
		Logger:                 logger.Default.LogMode(logger.Warn),
		SkipDefaultTransaction: true, // Evita BEGIN/COMMIT desnecessários em escritas simples
	})
	if err != nil {
		log.Fatalf("Erro ao conectar ao PostgreSQL via GORM: %v", err)
	}

	sqlDB, err := db.DB()
	if err == nil {
		sqlDB.SetMaxIdleConns(10)
		sqlDB.SetMaxOpenConns(25)
		sqlDB.SetConnMaxLifetime(30 * time.Minute)
	}

	log.Println("Executando GORM AutoMigrate no PostgreSQL...")
	if err := models.AutoMigrate(db); err != nil {
		log.Fatalf("Erro ao executar AutoMigrate: %v", err)
	}
	// Índices compostos para deixar as buscas do Modo Solo, Arena e Ranking instantâneas
	_ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_uqp_user_cycle ON user_question_progresses (user_id, answered_correctly_in_cycle)`).Error
	_ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_uqp_user_mastered ON user_question_progresses (user_id, mastered_ever)`).Error
	_ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_atqp_team_cycle ON arena_team_question_progresses (team_id, answered_correctly_in_cycle)`).Error
	log.Println("Tabelas e índices sincronizados com sucesso via GORM AutoMigrate!")

	hub := ws.NewHub()
	srv := handlers.NewServer(db, hub)

	mux := http.NewServeMux()

	// Rotas de Autenticação e Usuários
	mux.HandleFunc("/api/auth/register", srv.HandleRegister)
	mux.HandleFunc("/api/auth/login", srv.HandleLogin)
	mux.HandleFunc("/api/auth/logout", srv.HandleLogout)
	mux.HandleFunc("/api/auth/me", srv.HandleMe)
	mux.HandleFunc("/api/users", srv.HandleUsers)
	mux.HandleFunc("/api/admin/users/role", srv.HandlePromoteUser)

	// Rotas Colaborativas de Questões (Admins) + Verificação Assíncrona Anti-Duplicação
	mux.HandleFunc("/api/questions/check", srv.HandleCheckQuestionSimilarity)
	mux.HandleFunc("/api/questions", srv.HandleQuestions)
	mux.HandleFunc("/api/questions/", srv.HandleQuestionByID)
	mux.HandleFunc("/ws/questions", srv.HandleQuestionsWS)

	// Rotas do Modo Estudo Solo
	mux.HandleFunc("/api/solo/next", srv.HandleSoloNext)
	mux.HandleFunc("/api/solo/answer", srv.HandleSoloAnswer)

	// Rota do Ranking Global (Individual + Equipes)
	mux.HandleFunc("/api/ranking", srv.HandleRanking)

	// Rotas do Modo Arena por Equipe + WebSocket em Tempo Real
	mux.HandleFunc("/api/arena/teams", srv.HandleArenaTeams)
	mux.HandleFunc("/api/arena/teams/", srv.HandleArenaTeamRoutes)
	mux.HandleFunc("/ws/arena/", srv.HandleArenaWS)

	// Healthcheck para o Render
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Páginas HTML separadas (sem flash de tela de login ao recarregar!)
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if isAuthenticatedRequest(r) {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		http.ServeFile(w, r, "./web/login.html")
	})
	mux.HandleFunc("/arena", serveProtectedPage("./web/arena.html"))
	mux.HandleFunc("/solo", serveProtectedPage("./web/solo.html"))
	mux.HandleFunc("/questions", serveAdminPage(db, "./web/questions.html"))
	mux.HandleFunc("/ranking", serveProtectedPage("./web/ranking.html"))
	mux.HandleFunc("/monitor", serveProtectedPage("./web/monitor.html"))
	mux.HandleFunc("/play", serveProtectedPage("./web/play.html"))

	// Rota raiz "/" (Dashboard protegido) e arquivos estáticos (.css, .js)
	fs := http.FileServer(http.Dir("./web"))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			serveProtectedPage("./web/index.html")(w, r)
			return
		}
		fs.ServeHTTP(w, r)
	})

	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}

	addr := fmt.Sprintf("0.0.0.0:%s", port)
	log.Printf("Servidor Web Kenade rodando em http://localhost:%s", port)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Erro no servidor HTTP: %v", err)
	}
}
