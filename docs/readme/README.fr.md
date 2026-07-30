# Cohort

Agent Runtime en ligne de commande, local-first, pour les appels d'outils, l'automatisation du navigateur, la perception du bureau, les longs contextes, les SOP et la mémoire vérifiée.

**Langues :** [简体中文](../../README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · **Français** · [हिन्दी](README.hi.md)

## Qu'est-ce Que Cohort

Cohort est un Agent Runtime local écrit en Go. Il connecte des providers LLM OpenAI-compatible et Anthropic avec une couche d'outils contrôlée, des sessions persistantes, l'automatisation du navigateur, le Computer Use de bureau sur macOS, la compactation du contexte, le routage SOP et une mémoire longue durée vérifiée.

```text
Intention utilisateur
  -> Agent Loop
  -> Context Manager
  -> LLM tool calling
  -> Outils locaux / navigateur / bureau / shell
  -> Registre de preuves
  -> Historique de session et mémoire vérifiée
```

La règle principale : le modèle raisonne, mais l'exécution doit être explicite, auditable, récupérable et appuyée par des preuves.

## Démarrage Rapide

```bash
npm install -g @cohort-ai/cohort@latest
export DEEPSEEK_API_KEY="sk-xxx"
cohort
```

Exécuter une tâche :

```bash
cohort ask "lis README.md et résume les capacités actuelles du runtime"
```

Inspecter le runtime :

```bash
cohort config
cohort doctor
cohort session list
```

Exécuter depuis les sources pour le développement :

```bash
git clone https://github.com/congchuanling-dot/Cohort.git
cd Cohort
go build -o cohort ./cmd/cohort
./cohort
```

Le package npm est publié sur le registry npm public et télécharge le binaire macOS vérifié depuis GitHub Releases pendant l'installation. La configuration par défaut se trouve dans [`configs/config.yaml`](../../configs/config.yaml). Le guide d'utilisation complet est dans [`docs/usage.md`](../usage.md).

## Providers LLM

Cohort prend aujourd'hui en charge nativement deux familles de provider :

- `provider: openai` : endpoints OpenAI-compatible Chat Completions comme DeepSeek, Ollama, LM Studio et autres passerelles compatibles `/v1/chat/completions`
- `provider: anthropic` : Anthropic Messages API

`llm.profiles` et `fallback_profiles` permettent aussi de chaîner explicitement un provider principal et un provider de secours.

Cela ne veut pas dire que tous les types d'API fonctionnent déjà sans adaptation. Gemini natif, Bedrock, Vertex et les variantes d'authentification ou de chemin propres à Azure OpenAI n'ont pas encore d'adaptateur natif.

## Fonctionnalités

| Domaine | Capacité |
| --- | --- |
| Agent Loop | Chat streaming avec providers OpenAI-compatible / Anthropic, tool calling, contrôle du nombre de tours |
| Outils locaux | Lecture/écriture/patch de fichiers, shell, questions utilisateur, erreurs structurées |
| Navigateur | Chrome bridge, scan de page, JS, snapshot d'éléments, click/type/key/wait/screenshot/OCR |
| Desktop Computer Use | Permissions macOS, fenêtres, activation PID, captures, arbre AX, OCR bureau, `AXPress` contrôlé, touches restreintes, brouillon texte |
| Sessions | `history.jsonl`, métadonnées, liste, reprise, audit local |
| Context Manager | Compactation des résultats, découpage sûr, session memory, full compact |
| SOP Runtime | Index SOP, indices par tâche, working checkpoint |
| Evolution Memory | Entrées avec preuves, mémoire projet, déduplication, read-back, audit |

## CLI

```bash
cohort                         # mode interactif
cohort ask "task"              # exécuter une tâche puis quitter
cohort tools                   # lister les outils
cohort config                  # afficher la configuration active
cohort session list            # lister les sessions locales
cohort session resume <id>     # reprendre une session
```

Commandes interactives :

```text
/help
/model
/config
/tools
/session
/session list
/resume <session_id>
/compact
/full-compact
/memory
/exit
```

## Automatisation Du Navigateur

Cohort contrôle Chrome via un Browser Bridge local :

```text
ws://127.0.0.1:18777/browser
```

Flux recommandé :

```text
open -> wait -> snapshot -> click/type/key -> wait -> verify
```

Utilisez `browser_ocr` uniquement lorsque le texte DOM et `browser_dom_summary` ne peuvent pas lire le texte rendu. Les bbox OCR sont `screenshot-local` et ne sont pas des coordonnées souris système.

Dépendances OCR optionnelles :

```bash
python3 -m pip install rapidocr-onnxruntime pillow numpy
```

Si les outils navigateur retournent `browser_not_connected`, chargez l'extension Chrome depuis `assert/cohort_browser_bridge`.

## Desktop Computer Use

Cohort fournit une perception générique du bureau macOS et des actions sémantiques AX contrôlées :

```text
desktop_permissions
  -> desktop_windows
  -> desktop_activate
  -> desktop_ax_snapshot
  -> desktop_screenshot
  -> desktop_ocr
  -> desktop_ax_press
  -> desktop_press_key
  -> desktop_type_text
```

Accessibility / AX est prioritaire sur l'OCR. `desktop_ax_press` exige un PID au premier plan, des métadonnées AX fraîches, une revalidation avant action et une vérification AX après action. `desktop_press_key` n'accepte qu'une allowlist de touches ; les touches de navigation à faible risque peuvent être exécutées directement, tandis que Enter, Cmd+Enter, Delete, Backspace et équivalents exigent une confirmation. `desktop_type_text` ne fait que rédiger du texte dans le champ éditable actuellement focus, sans l'envoyer.

Politique de risque :

- R1 actions réversibles : exécution directe possible.
- R2 effets externes : token de confirmation unique émis par `ask_user`.
- R3 actions à haut risque comme paiement, approbation, autorisation, vérification de connexion ou suppression : refus automatique pour exécution manuelle.

Il n'existe pas encore de clic par coordonnées.

Dépendances du helper macOS :

```bash
python3 -m pip install pyobjc-framework-Quartz pyobjc-framework-Cocoa pyobjc-framework-ApplicationServices
```

Accordez les permissions Accessibility et Screen Recording au terminal ou à l'IDE qui exécute Cohort.

## Mémoire Et SOP

La mémoire longue durée suit un pipeline strict en trois étapes :

```text
start_long_term_update
  -> memory_propose_update
  -> memory_apply_update
```

Les écritures doivent référencer des preuves vérifiées, refuser le contenu sensible ou dupliqué, puis relire l'entrée avant succès.

Les SOP sont des contraintes opérationnelles légères. Cohort injecte [`sops/index.md`](../../sops/index.md) comme navigation et demande de lire le SOP pertinent avant d'agir.

## Développement

```bash
go test ./...
go vet ./...
go run . tools
go run . config
```

## Principes

- Local first.
- Outils auditables.
- Historique immuable.
- Contexte par couches.
- Mémoire vérifiée.
- Évolution progressive.
