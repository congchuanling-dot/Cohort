# Cohert

Agent Runtime en ligne de commande, local-first, pour les appels d'outils, l'automatisation du navigateur, la perception du bureau, les longs contextes, les SOP et la mémoire vérifiée.

**Langues :** [简体中文](../../README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · **Français** · [हिन्दी](README.hi.md)

## Qu'est-ce Que Cohert

Cohert est un Agent Runtime local écrit en Go. Il connecte un LLM compatible OpenAI, une couche d'outils contrôlée, des sessions persistantes, l'automatisation du navigateur, le Computer Use de bureau sur macOS, la compactation du contexte, le routage SOP et une mémoire longue durée vérifiée.

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
git clone <repo-url>
cd Cohort
export DEEPSEEK_API_KEY="sk-xxx"
go run .
```

Exécuter une tâche :

```bash
go run . ask "lis README.md et résume les capacités actuelles du runtime"
```

Inspecter le runtime :

```bash
go run . config
go run . tools
go run . session list
```

Compiler :

```bash
go build -o cohert ./cmd/cohert
./cohert
```

La configuration par défaut se trouve dans [`configs/config.yaml`](../../configs/config.yaml). Le guide complet est dans [`docs/usage.md`](../usage.md).

## Fonctionnalités

| Domaine | Capacité |
| --- | --- |
| Agent Loop | Chat streaming compatible OpenAI, tool calling, contrôle du nombre de tours |
| Outils locaux | Lecture/écriture/patch de fichiers, shell, questions utilisateur, erreurs structurées |
| Navigateur | Chrome bridge, scan de page, JS, snapshot d'éléments, click/type/key/wait/screenshot/OCR |
| Desktop Computer Use | Permissions macOS, fenêtres, activation PID, captures, arbre AX, OCR bureau, `AXPress` contrôlé |
| Sessions | `history.jsonl`, métadonnées, liste, reprise, audit local |
| Context Manager | Compactation des résultats, découpage sûr, session memory, full compact |
| SOP Runtime | Index SOP, indices par tâche, working checkpoint |
| Evolution Memory | Entrées avec preuves, mémoire projet, déduplication, read-back, audit |

## CLI

```bash
cohert                         # mode interactif
cohert ask "task"              # exécuter une tâche puis quitter
cohert tools                   # lister les outils
cohert config                  # afficher la configuration active
cohert session list            # lister les sessions locales
cohert session resume <id>     # reprendre une session
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

Cohert contrôle Chrome via un Browser Bridge local :

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

Si les outils navigateur retournent `browser_not_connected`, chargez l'extension Chrome depuis `assert/cohert_browser_bridge`.

## Desktop Computer Use

Cohert fournit une perception générique du bureau macOS et des actions sémantiques AX contrôlées :

```text
desktop_permissions
  -> desktop_windows
  -> desktop_activate
  -> desktop_ax_snapshot
  -> desktop_screenshot
  -> desktop_ocr
  -> desktop_ax_press
```

Accessibility / AX est prioritaire sur l'OCR. `desktop_ax_press` est aujourd'hui la seule action d'entrée desktop. Elle exige un PID au premier plan, des métadonnées AX fraîches, une revalidation avant action et une vérification AX après action.

Politique de risque :

- R1 actions réversibles : exécution directe possible.
- R2 effets externes : token de confirmation unique émis par `ask_user`.
- R3 actions à haut risque comme paiement, approbation, autorisation, vérification de connexion ou suppression : refus automatique pour exécution manuelle.

Il n'existe pas encore de clic par coordonnées, clavier ou saisie texte desktop.

Dépendances du helper macOS :

```bash
python3 -m pip install pyobjc-framework-Quartz pyobjc-framework-Cocoa pyobjc-framework-ApplicationServices
```

Accordez les permissions Accessibility et Screen Recording au terminal ou à l'IDE qui exécute Cohert.

## Mémoire Et SOP

La mémoire longue durée suit un pipeline strict en trois étapes :

```text
start_long_term_update
  -> memory_propose_update
  -> memory_apply_update
```

Les écritures doivent référencer des preuves vérifiées, refuser le contenu sensible ou dupliqué, puis relire l'entrée avant succès.

Les SOP sont des contraintes opérationnelles légères. Cohert injecte [`sops/index.md`](../../sops/index.md) comme navigation et demande de lire le SOP pertinent avant d'agir.

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
