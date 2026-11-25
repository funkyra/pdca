# PDCA

[English](README.md) · [Русский](README.ru.md) · [Español](README.es.md)

PDCA est une compétence pour Claude Code et Codex qui organise les modifications de code importantes selon le cycle **Plan → Do → Check → Act** (planifier, réaliser, vérifier, ajuster). Décrivez le résultat attendu. PDCA découpe le travail en tâches, les réalise dans des commits Git distincts et vérifie le résultat selon les critères d’acceptation. Si la revue échoue, PDCA lance une correction ou une révision du plan.

PDCA convient aux changements qui bénéficient de dépendances explicites et d’une revue indépendante, par exemple une fonctionnalité qui touche plusieurs modules, une migration ou une refactorisation soumise à des exigences de compatibilité. Si vous interrompez l’exécution, vous pouvez la reprendre dans une nouvelle session et consulter le travail effectué ainsi que les décisions prises par les agents.

## Prérequis

- Un shell de type Unix et Go 1.22 ou une version ultérieure. Le script `scripts/pdca` compile le CLI à la première utilisation et le recompile lorsque les sources Go changent.
- Git et un dépôt cible avec au moins un commit, un arbre de travail propre et une identité d’auteur configurée pour les commits.
- Le CLI de Beads (`bd`) dans le `PATH`. Consultez le [guide d’installation de Beads](https://github.com/gastownhall/beads/blob/main/docs/getting-started/installation.md).
- Une session Claude Code ou Codex capable de lancer des sous-agents personnalisés. Pour utiliser `pdca once`, l’exécutable `claude` ou `codex` doit être dans le `PATH`.

## Installation et lancement

### Installer la compétence

```sh
git clone https://github.com/funkyra/pdca.git
cd pdca
./scripts/pdca install
export PATH="$HOME/.local/bin:$PATH"
```

Le programme d’installation crée des liens symboliques vers la compétence dans `~/.claude/skills/pdca` et `~/.agents/skills/pdca` et ajoute un lien vers le script dans `~/.local/bin/pdca`. Conservez le dépôt cloné à cet emplacement pour que les liens restent valides. Si `~/.local/bin` figure déjà dans votre `PATH`, vous pouvez omettre la ligne `export`. Sinon, ajoutez-la à la configuration de votre shell pour accéder à `pdca` dans les nouveaux terminaux.

### Configurer le dépôt à modifier

Ouvrez une nouvelle session Claude Code ou Codex dans le **dépôt cible** et utilisez les commandes du client choisi.

| Action | Claude Code | Codex |
| --- | --- | --- |
| Configurer le dépôt | `/pdca init` | `$pdca init` |
| Lancer une tâche | `/pdca <tâche>` | `$pdca <tâche>` |
| Reprendre une exécution interrompue | `/pdca resume` | `$pdca resume` |

`init` initialise Beads si nécessaire, demande le modèle et le niveau d’effort de raisonnement de chaque phase s’ils manquent, et crée les agents dans le dépôt, sous `.claude/agents/` pour Claude Code ou `.codex/agents/` pour Codex. Vérifiez et validez dans Git les modifications de configuration indiquées dans la sortie, puis ouvrez une nouvelle session pour charger les agents. L’arbre de travail doit être propre avant la première exécution.

Vous pouvez aussi configurer le dépôt cible depuis le terminal. Cet exemple utilise les alias de modèles de Claude Code.

```sh
pdca init --harness claude \
  --models 'plan=opus/high do=sonnet/medium check=sonnet/high act=haiku/medium' \
  --review epic
```

### Confier une tâche à PDCA

Voici un exemple de demande dans une session Codex.

```text
$pdca Ajoute un mode de simulation à l’import CSV. Affiche le nombre de lignes à insérer ou à ignorer, conserve les données enregistrées et couvre les deux modes avec des tests.
```

Avant de commencer, PDCA vérifie Beads, l’arbre de travail, les agents et la configuration des modèles. Depuis une branche protégée comme `main`, PDCA bascule sur une branche `pdca/<nom-court-de-la-tâche>`. Si vous travaillez déjà sur une branche de développement, PDCA continue sur cette branche. Le rapport final indique la branche, le nombre de commits, les tickets de suivi, ainsi que les décisions et hypothèses consignées par les agents. Les changements restent sur la branche locale pour que vous puissiez les examiner.

### Lancer une exécution ponctuelle depuis le terminal

Une fois la compétence installée, exécutez l’une de ces commandes depuis le dépôt cible pour ouvrir une session avec la tâche.

```sh
pdca once --harness claude 'Ajoute un mode de simulation à l’import CSV'
pdca once --harness codex 'Ajoute un mode de simulation à l’import CSV'
```

`once` initialise Beads si nécessaire, puis démarre le client choisi avec la tâche. Il fournit les agents de phase à la session Claude Code ou les installe dans le dossier `.codex/agents/` du dépôt pour Codex. Les modèles des phases restent nécessaires. Placez les arguments supplémentaires du client après `--`.

## Fonctionnement

La session principale lance un nouvel agent à chaque phase. Le CLI écrit en Go vérifie le résultat de la phase et indique à la session principale quel agent lancer ensuite. Les instructions de Plan et Check s’appuient sur les outils de graphe de Codebase Memory pour explorer le code du dépôt cible.

| Phase | Rôle |
| --- | --- |
| **Plan** | Transforme la demande en un « epic » Beads qui regroupe la conception, les critères d’acceptation, les sous-tâches et leurs dépendances. Consigne dans l’epic les hypothèses et les décisions de conception. |
| **Do** | Implémente et teste une tâche. Chaque cycle de réalisation ou de correction réussi produit un commit dont le message se termine par une ligne `Bead: <task-id>`. |
| **Check** | Examine le diff, lance les tests et compare le résultat aux critères d’acceptation. Consigne un verdict `PASS` ou `FAIL` et vérifie que le résultat intégré respecte les critères de l’epic. |
| **Act** | Analyse les constats après un `FAIL`. Peut clore le travail, confier une correction précise à Do, demander une révision du plan, créer des tickets de suivi ou solliciter une décision de l’utilisateur. |

Beads conserve l’epic, les dépendances des tâches, les références de commits, les revues et les décisions. Git conserve les modifications du code. Le répertoire `.pdca/`, ignoré par Git, contient l’état local, dont la phase en cours. À la reprise, PDCA utilise cet état, Beads et Git pour vérifier où le travail s’est arrêté et déterminer la phase suivante.

## Configuration

PDCA lit le premier fichier `AGENTS.md` qui contient une section `PDCA models` pour le client choisi. La recherche commence à la racine du dépôt, remonte dans ses répertoires parents, puis consulte `~/.codex/AGENTS.md` par défaut. `pdca init --models` écrit cette section dans le fichier `AGENTS.md` du dépôt. Si aucune section ne correspond, `pdca models --set` permet de sauvegarder les paramètres localement dans `.pdca/`.

```markdown
## PDCA models: claude
- plan: opus high
- do: sonnet medium
- check: sonnet high
- act: haiku medium
- review: epic
- parallel: no
```

Pour Codex, utilisez `## PDCA models: codex` avec les identifiants de modèles et les niveaux d’effort de raisonnement pris en charge par votre session. Les quatre lignes correspondant aux phases sont obligatoires. Vous pouvez personnaliser `do_L` et `check_L` pour les tâches de taille `L`, `epic_check` et `epic_act` pour les phases portant sur l’epic entier, et `merge` pour l’intégration après un lot parallèle. Par défaut, `merge` reprend les paramètres de `check`.

| Paramètre | Comportement |
| --- | --- |
| `review: epic` (par défaut) | Check examine l’ensemble de l’epic une fois les commits des tâches créés. Act peut ajouter des tâches de correction si la revue échoue. |
| `review: task` | Check examine le commit de chaque tâche. Lors de la dernière tâche, Check vérifie aussi le résultat intégré de l’epic. |
| `parallel: no` (par défaut) | Do exécute les tâches une par une. |
| `parallel: yes` | Lorsque plusieurs tâches sont prêtes, PDCA en lance jusqu’à huit en même temps dans des arbres de travail Git distincts issus du même commit. Les dépendances déterminent quelles tâches peuvent être regroupées. Avant Check, un agent Merge intègre les commits à la branche de travail, résout les conflits et élimine les doublons de code. |

## Consulter ou reprendre une exécution

Exécutez ces commandes depuis le dépôt cible.

| Commande | Informations ou action |
| --- | --- |
| `pdca models --harness codex` | Affiche les modèles utilisés, les niveaux d’effort de raisonnement, le mode de revue et la source de configuration de Codex. Utilisez `claude` pour Claude Code. |
| `pdca current` | Indique l’epic ouvert pour la branche actuelle. |
| `pdca status <epic-id>` | Affiche l’état des tâches, les références de commits et les verdicts des revues. |
| `pdca brief <epic-or-task-id>` | Affiche le plan enregistré ou les détails d’une tâche, notamment les critères d’acceptation. |
| `pdca report <epic-id>` | Affiche la branche, le nombre de commits, les tickets de suivi et les décisions consignées. Exécutez la commande sur la branche de l’epic pour obtenir le bon nombre de commits. |
| `pdca next [<epic-id>]` | Vérifie la phase en cours et indique laquelle lancer ensuite. Pour reprendre le travail depuis le client, utilisez `/pdca resume` ou `$pdca resume`. |

- `FAIL no-beads` signifie que les vérifications préalables n’ont trouvé aucun espace de travail Beads. Assurez-vous que `bd` est dans le `PATH`, puis lancez la commande `init` du client.
- `FAIL dirty-tree` énumère les changements locaux. Créez un commit ou mettez ces changements de côté avant de réessayer.
- `FAIL agents-stale` affiche la commande de mise à jour des définitions des agents. Exécutez-la, puis ouvrez une nouvelle session.
- `FAIL no-models` signifie que les modèles des phases manquent. Renseignez-les dans `AGENTS.md` ou utilisez la commande `models --set` indiquée à l’issue des vérifications préalables.
- `STUCK` signale des tâches ouvertes dont les dépendances restent à satisfaire. `ESCALATE` demande une décision pour pouvoir poursuivre le travail.

## Structure du dépôt et tests

| Chemin | Contenu |
| --- | --- |
| [`SKILL.md`](SKILL.md) | Instructions pour la session principale qui coordonne l’exécution. |
| [`phases/`](phases/) | Instructions de configuration, de travail des agents et de planification des tâches parallèles. |
| [`scripts/pdca`](scripts/pdca) | Script shell qui compile et lance le CLI. |
| [`scripts/src/`](scripts/src/) | CLI en Go, vérification du déroulement des phases, intégration avec Beads et Git, et tests. |
| [`evals/evals.json`](evals/evals.json) | Scénarios d’évaluation de la compétence et résultats attendus. |

Lancez les tests Go depuis le dépôt PDCA cloné.

```sh
cd scripts/src
go test ./...
```

Les tests qui utilisent Beads sont ignorés si `bd` est indisponible.

## Licence

[Mozilla Public License 2.0](LICENSE).
