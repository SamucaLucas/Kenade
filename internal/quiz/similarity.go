package quiz

import (
	"Kenade/internal/models"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"unicode"

	"gorm.io/gorm"
)

// SimilarMatch representa uma questão já existente no banco que é idêntica ou muito parecida no ENUNCIADO.
type SimilarMatch struct {
	Question          models.Question `json:"question"`
	Similarity        float64         `json:"similarity"`         // 0.0 a 1.0
	SimilarityPercent int             `json:"similarity_percent"` // 0 a 100
	IsExact           bool            `json:"is_exact"`
	MatchReason       string          `json:"match_reason"`
}

// Stopwords do português + termos genéricos de comando de prova do ENADE para não acusar falso positivo
var ptStopwords = map[string]bool{
	"a": true, "ao": true, "aos": true, "aquela": true, "aquelas": true, "aquele": true, "aqueles": true,
	"aquilo": true, "as": true, "ate": true, "com": true, "como": true, "da": true, "das": true, "de": true,
	"dela": true, "delas": true, "dele": true, "deles": true, "depois": true, "do": true, "dos": true,
	"e": true, "ela": true, "elas": true, "ele": true, "eles": true, "em": true, "entre": true, "era": true,
	"eram": true, "essa": true, "essas": true, "esse": true, "esses": true, "esta": true, "estas": true,
	"este": true, "estes": true, "isto": true, "ja": true, "mais": true, "mas": true, "mesmo": true,
	"na": true, "nas": true, "nao": true, "no": true, "nos": true, "o": true, "os": true, "ou": true,
	"para": true, "pela": true, "pelas": true, "pelo": true, "pelos": true, "por": true, "qual": true,
	"quando": true, "que": true, "quem": true, "se": true, "sem": true, "ser": true, "seu": true,
	"seus": true, "so": true, "sobre": true, "sua": true, "suas": true, "tambem": true, "um": true,
	"uma": true, "umas": true, "uns": true,
	// Termos padrão de cabeçalho/comando de questões ENADE
	"considerando": true, "contexto": true, "avalie": true, "afirmacoes": true, "afirmacao": true,
	"assercoes": true, "assercao": true, "seguir": true, "correto": true, "afirma": true,
	"assinale": true, "alternativa": true, "correta": true, "respeito": true, "acerca": true,
	"apenas": true, "texto": true, "apresentado": true, "analise": true, "itens": true,
}

func removeAccents(r rune) rune {
	switch unicode.ToLower(r) {
	case 'á', 'à', 'ã', 'â', 'ä':
		return 'a'
	case 'é', 'è', 'ê', 'ë':
		return 'e'
	case 'í', 'ì', 'î', 'ï':
		return 'i'
	case 'ó', 'ò', 'õ', 'ô', 'ö':
		return 'o'
	case 'ú', 'ù', 'û', 'ü':
		return 'u'
	case 'ç':
		return 'c'
	case 'ñ':
		return 'n'
	default:
		return unicode.ToLower(r)
	}
}

// NormalizeText normaliza um texto removendo acentos, pontuação e espaços duplicados.
func NormalizeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := true

	for _, r := range s {
		clean := removeAccents(r)
		if unicode.IsLetter(clean) || unicode.IsDigit(clean) {
			b.WriteRune(clean)
			lastSpace = false
		} else if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

// ComputeStatementHash gera o hash SHA-256 canônico apenas do enunciado normalizado.
func ComputeStatementHash(statement string) string {
	norm := NormalizeText(statement)
	if norm == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:])
}

func extractSignificantTokens(normText string) map[string]bool {
	words := strings.Fields(normText)
	tokens := make(map[string]bool, len(words))
	for _, w := range words {
		if len(w) <= 2 || ptStopwords[w] {
			continue
		}
		tokens[w] = true
	}
	return tokens
}

func extractTrigrams(normText string) map[string]int {
	compact := strings.ReplaceAll(normText, " ", "")
	runes := []rune(compact)
	trigrams := make(map[string]int)
	if len(runes) < 3 {
		if len(runes) > 0 {
			trigrams[string(runes)] = 1
		}
		return trigrams
	}
	for i := 0; i <= len(runes)-3; i++ {
		tg := string(runes[i : i+3])
		trigrams[tg]++
	}
	return trigrams
}

func diceTrigramSimilarity(a, b map[string]int) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	totalA := 0
	totalB := 0
	for k, va := range a {
		totalA += va
		if vb, ok := b[k]; ok {
			if va < vb {
				intersection += va
			} else {
				intersection += vb
			}
		}
	}
	for _, vb := range b {
		totalB += vb
	}
	if totalA+totalB == 0 {
		return 0
	}
	return (2.0 * float64(intersection)) / float64(totalA+totalB)
}

func tokenSimilarity(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for k := range a {
		if b[k] {
			intersection++
		}
	}
	if intersection == 0 {
		return 0
	}
	union := len(a) + len(b) - intersection
	jaccard := float64(intersection) / float64(union)

	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	containment := float64(intersection) / float64(minLen)

	return (jaccard * 0.65) + (containment * 0.35)
}

// CheckQuestionSimilarity compara APENAS O ENUNCIADO (statement) contra todas as questões do banco,
// ignorando completamente as alternativas (pois muitas questões de ENADE possuem alternativas iguais como "I e IV, apenas").
func CheckQuestionSimilarity(db *gorm.DB, statement string, excludeID uint) (*SimilarMatch, []SimilarMatch, error) {
	normCandidateStmt := NormalizeText(statement)
	if len(normCandidateStmt) < 6 {
		return nil, nil, nil
	}

	candidateHash := ComputeStatementHash(statement)
	candidateTokens := extractSignificantTokens(normCandidateStmt)
	candidateTrigrams := extractTrigrams(normCandidateStmt)

	var existing []models.Question
	query := db.Preload("CreatedBy")
	if excludeID > 0 {
		query = query.Where("id <> ?", excludeID)
	}
	if err := query.Find(&existing).Error; err != nil {
		return nil, nil, err
	}

	var exactMatch *SimilarMatch
	similarMatches := make([]SimilarMatch, 0)

	for _, q := range existing {
		normExistingStmt := NormalizeText(q.Statement)
		if normExistingStmt == "" {
			continue
		}

		// 1. Verifica igualdade exata do enunciado normalizado ou hash
		if (q.StatementHash != "" && q.StatementHash == candidateHash) || normExistingStmt == normCandidateStmt {
			match := SimilarMatch{
				Question:          q,
				Similarity:        1.0,
				SimilarityPercent: 100,
				IsExact:           true,
				MatchReason:       "Enunciado 100% idêntico ao já cadastrado no banco",
			}
			exactMatch = &match
			break
		}

		existingTokens := extractSignificantTokens(normExistingStmt)
		existingTrigrams := extractTrigrams(normExistingStmt)

		trigramSim := diceTrigramSimilarity(candidateTrigrams, existingTrigrams)
		wordSim := tokenSimilarity(candidateTokens, existingTokens)

		// Similaridade baseada exclusivamente no enunciado
		stmtScore := (trigramSim * 0.50) + (wordSim * 0.50)
		if stmtScore > 1.0 {
			stmtScore = 1.0
		}

		pct := int(stmtScore*100.0 + 0.5)

		// >= 95% de similaridade no enunciado é tratado como duplicata idêntica
		if stmtScore >= 0.95 {
			match := SimilarMatch{
				Question:          q,
				Similarity:        stmtScore,
				SimilarityPercent: pct,
				IsExact:           true,
				MatchReason:       "Enunciado praticamente idêntico (95%+ de igualdade textual)",
			}
			exactMatch = &match
			break
		}

		// Entre 48% e 94% de semelhança no enunciado (com boa sobreposição de termos significativos do enunciado)
		if stmtScore >= 0.48 && wordSim >= 0.35 {
			similarMatches = append(similarMatches, SimilarMatch{
				Question:          q,
				Similarity:        stmtScore,
				SimilarityPercent: pct,
				IsExact:           false,
				MatchReason:       "Enunciado com texto e conceitos muito parecidos",
			})
		}
	}

	sort.Slice(similarMatches, func(i, j int) bool {
		return similarMatches[i].Similarity > similarMatches[j].Similarity
	})

	if len(similarMatches) > 3 {
		similarMatches = similarMatches[:3]
	}

	return exactMatch, similarMatches, nil
}
