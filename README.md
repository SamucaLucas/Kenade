<div align="center">
  <img src="web/logo.svg" alt="Kenade — ENADE Quiz" width="320" />

  <h1>🎓 KENADE — Plataforma Colaborativa de Estudos para o ENADE</h1>

  <p>
    <strong>Estude em equipe no estilo Kahoot projetado no monitor ou revise sozinho pelo celular em um grande Quiz Único colaborativo.</strong>
  </p>

  <p>
    <img src="https://img.shields.io/badge/Foco-ENADE-2563EB?style=for-the-badge" alt="Foco ENADE" />
    <img src="https://img.shields.io/badge/Modo-Arena_%26_Solo-7C3AED?style=for-the-badge" alt="Modo Arena e Solo" />
    <img src="https://img.shields.io/badge/Cria%C3%A7%C3%A3o-Colaborativa-059669?style=for-the-badge" alt="Criação Colaborativa" />
    <img src="https://img.shields.io/badge/Ranking-Global_%26_Equipes-F59E0B?style=for-the-badge" alt="Ranking Global e Equipes" />
  </p>
</div>

---

## 💡 A Proposta do Projeto

O **Kenade** nasceu de uma necessidade real de preparação entre amigos para a prova do **ENADE**. Ferramentas tradicionais de quiz como o Kahoot possuem limitações severas de quantidade de caracteres (inviabilizando enunciados longos típicos do ENADE) e não permitem que vários amigos alimentem e estudem simultaneamente em um único grande banco colaborativo de questões com controle inteligente de progresso.

O **Kenade** resolve isso unindo a **dinâmica competitiva de sala (Monitor + Celular)** com um **modo de estudo individual completo**, mantendo um único grande questionário contínuo onde nenhuma pergunta respondida corretamente se repete até que todo o banco seja dominado.

---

## ✨ Principais Funcionalidades

### 🤝 1. Criação Colaborativa de Questões (Sem Limite Curto de Texto)
- **Quiz Único Gigante:** Todas as perguntas alimentam um único grande questionário oficial da plataforma.
- **Trabalho em Equipe entre Administradores:** Os administradores podem injetar, editar e revisar questões simultaneamente.
- **Textos Completos no Padrão ENADE:** Suporte a enunciados extensos, textos-base, asserções (`I, II, III`) e 4 alternativas completas (`A`, `B`, `C`, `D`), além de **Observação / Gabarito Comentado** opcional para revisão após a resposta.
- **Tempos Dinâmicos:** Escolha rápida entre **30s**, **45s (padrão)** ou **60s** por questão.

---

### 📋 2. Ordem Oficial do ENADE & Ciclo Inteligente Anti-Repetição
- **1º Formação Geral → 2º Componente Específico:** Cada pergunta é classificada como **Geral** ou **Específica**. O sistema segue rigorosamente a estrutura da prova: primeiro sorteia todas as questões **Gerais** pendentes e, somente após concluí-las, avança para as questões **Específicas**.
- **Zero Repetição Desnecessária:** Questões já respondidas corretamente não voltam a aparecer no ciclo atual.
- **Foco nos Pontos Fracos:** Questões ainda não respondidas ou erradas anteriormente continuam no ciclo até serem dominadas.
- **Replay Automático (Ciclos):** Ao gabaritar 100% das perguntas do banco, o sistema registra um novo **Ciclo Concluído** e reinicia a jornada para fixação do conteúdo.

---

### 🖥️📱 3. Modo Arena em Equipe (Monitor + Celulares)
Ideal para sessões de estudo em grupo, presencialmente ou em chamada:
- **Salas Privadas por Equipe:** O criador da sala pesquisa os amigos pelo `@usuário` e monta a equipe. Apenas quem faz parte da equipe visualiza e acessa a sala.
- **Progresso Exclusivo da Equipe:** Cada equipe possui seu próprio progresso começando do zero na sala, independente do progresso individual do Modo Solo.
- **Conexão Rápida via PIN ou Câmera (QR Code):** Os jogadores entram na sala digitando o PIN de 4 dígitos ou escaneando o QR Code expansível projetado no monitor diretamente pela câmera do celular.
- **Tela do Monitor (Projetor / TV em Tela Cheia):**
  - **Lobby ao Vivo:** Mostra quem já entrou na sala em tempo real.
  - **Fase de Leitura ("🚀 COMEÇOU!"):** Exibe primeiro apenas o enunciado da questão para leitura atenta antes de liberar as alternativas e o cronômetro.
  - **Gráfico de Barras de Votos:** Quando todos respondem ou o tempo acaba, um painel em destaque exibe a distribuição de votos em cada alternativa (`A`, `B`, `C`, `D`), a resposta correta e a observação comentada.
  - **Placar Top 5 da Equipe:** Entre uma questão e outra, exibe o ranking atualizado dos 5 primeiros colocados da sala.
- **Tela do Celular (Controle do Jogador):**
  - Exibe o enunciado e os 4 botões coloridos com o **texto completo das alternativas** para facilitar a leitura mesmo longe do monitor.
  - Ao escolher uma opção, exibe uma tela de espera até que todos respondam e, em seguida, revela se você acertou ou errou, os pontos ganhos pela velocidade, sequência de acertos (*streak* 🔥) e a observação da questão.

---

### 📱 4. Modo Estudo Solo (100% pelo Celular)
Para estudar sozinho a qualquer hora e em qualquer lugar:
- Perguntas e alternativas exibidas diretamente na tela do celular.
- Feedback instantâneo a cada resposta com exibição do gabarito e da observação comentada.
- Progresso salvo automaticamente na sua conta, alimentando sua posição no Ranking Global.

---

### 🏆 5. Duplo Sistema de Ranking Global
- **👤 Ranking Individual de Jogadores:** Classificado pela maior quantidade de **Acertos Únicos** (questões dominadas no banco), exibindo também o **Total de Acertos Acumulados**, quantidade de **Ciclos Zerados (Replays)** e taxa de aproveitamento (`%`).
- **🛡️ Ranking Exclusivo de Equipes:** Classifica as equipes pelo nome da sala com base nas questões dominadas em conjunto, total de acertos, ciclos concluídos e pontuação somada dos integrantes.

---

## 🎨 Identidade Visual & Experiência de Uso
- **Modo Escuro por Padrão:** Pensado para longas sessões de leitura e estudo sem cansar a vista, com alternância rápida para o Modo Claro a qualquer momento.
- **Design Limpo no Celular:** Barra superior compacta com **Aba Lateral Expansível (*Drawer*)** para dedicar o máximo de espaço da tela às perguntas e alternativas.
