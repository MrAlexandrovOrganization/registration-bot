# Эксплуатация и доставка frontend

## CI/CD

Репозиторий независимый, пока не опубликован. Workflow `.github/workflows/ci.yml`
запускает на PR/push main `make install`, `make check`, `make test-race`, `make build`
и отдельный job `make compose-build`. Проверки используют синтетические значения,
fake Telegram, не подключаются к production. `config-check` проверяет polling и webhook.

Deploy зависит от успешных check/build, только push main и **repository variable
`DEPLOY_ENABLED=true`**. По умолчанию пропущен. Все jobs используют `ubuntu-24.04`,
как notes-bot и subscription-catalog. Deploy использует environment `production`:
в нём можно хранить environment secrets и настроить protection rules, например
required reviewers и разрешённую ветку main. Само `environment: production` не включает
approval — он требуется только при настроенном правиле. Repository/environment
secrets этого репозитория:

| Secret | Назначение |
|---|---|
| `VM_HOST` | SSH hostname/IP, порт 22 |
| `VM_USER` | Пользователь VM с Docker-доступом, общий с backend для deploy lock |
| `VM_SSH_KEY` | Приватный SSH ключ Actions → VM |
| `VM_PROJECT_PATH` | Абсолютный путь frontend checkout, обычно `/home/maxim/projects/telegram-bots/registration-bot` |

Pinning SSH host key для подключения Actions → VM не настроен.

Provisioning: Git, Bash, Make, flock/util-linux, Docker Compose v2 с `up --wait --wait-timeout`,
доступ к registry/GitHub; чистый независимый clone main с origin этого репозитория.
Private origin требует отдельного read-only deploy key VM → GitHub и
known_hosts. `.env` хранится только на VM с ограниченными правами. Нужны backend,
`registration-api`, `kafka-net`, `jaeger-net`, `prometheus-net`, `telegram-net`
и broadcast-топик. Local Telegram Bot API должен работать в local mode и быть
доступен как `telegram-bot-api:8081` в `telegram-net`; frontend и явные webhook-команды
используют этот API origin.

SSH-шаг workflow проверяет корень/чистоту/main (включая untracked-файлы, кроме
игнорируемых), получает main из origin и сверяет FETCH_HEAD с проверенным SHA.
После fast-forward строго на этот SHA проверяет HEAD; устаревшему workflow,
divergent checkout или локальным правкам отказывает. Затем вызывает `make up`, как
локально: пересобирает образ и ждёт Compose healthcheck до 180 секунд
(`--wait --wait-timeout 180`). Проверяется `/readyz`; предусмотрено ожидание
предыдущего sender lease до 90 секунд. CI не публикует образ.
GitHub сериализует deploy jobs; `$HOME/.registration-deploy.lock` под общим VM_USER
сериализует оба репозитория, но не устанавливает порядок совместимых версий.
Frontend CD не регистрирует webhook, не создаёт топики, не запускает импорт/миграции.
Миграции применяет backend `make up` до запуска backend; frontend обновляется после него.

## Первый запуск и migration 004

Держать CD выключенным в **обоих** репозиториях до provisioning и первого
согласованного переключения. Полный runbook — backend `docs/OPERATIONS.md`,
импорт — backend `docs/MIGRATION.md`, контракт — backend `docs/INTERACTIVE.md`.
Эти документы не нужны для независимой сборки frontend.

Остановить старый frontend/Python poller, затем backend; сделать backup PostgreSQL,
выбрать пару SHA с зелёным CI и одинаковым proto. Обновить и собрать оба checkout.
Сначала выполнить backend `make up`: он собирает образ, останавливает backend,
выполняет `database-up`, затем `migrate-compose` (включая migration 004) и запускает backend.
После успешного завершения выполнить новый frontend `make up`; frontend миграции не запускает.
Нельзя включать новый backend со старым frontend: Kafka теперь broadcast-only,
интерактивная доставка требует Accept delivery IDs и PendingInteractive recovery.
Не сбрасывать старые jobs/leases. До 90 секунд может занимать освобождение sender lease.
Только после проверки direct/recovery/edit/broadcast включить DEPLOY_ENABLED=true.

## Webhook и polling

По умолчанию `WEBHOOK_URL` пустой, polling. Health/metrics — `127.0.0.1:9093`.
Webhook — `127.0.0.1:9083` → `:8080` контейнера, только за TLS reverse proxy на хосте.
Контейнерный proxy не может обращаться к loopback хоста как к своему localhost.
Настроить HTTPS URL, точный путь, сохранение секретного заголовка без логирования,
лимит тела 1 MiB и proxy timeout больше 20 секунд. Не использовать health-порт для updates.

Polling → webhook:
1. Остановить единственный poller (`make down`, без `-v`).
2. В `.env` задать `WEBHOOK_URL` и отдельный `TELEGRAM_WEBHOOK_SECRET`.
3. Собрать актуальный образ (`make compose-build`), подготовить proxy и запустить
   `make up`; проверить readiness и HTTPS маршрут.
4. Явно выполнить `make register-webhook`. CLI сохраняет pending updates и ставит
   max_connections=1. Проверить контролируемый update и ответ.

Webhook → polling: остановить frontend; явный `make delete-webhook` на собранном
образе сохраняет pending updates. Очистить WEBHOOK_URL, убрать proxy route и запустить
`make up`. Не запускать два receiver для одного token. Только команды
`register-webhook`/`delete-webhook` используют `compose run --rm --no-deps`
и не запускают приложение/зависимости.

## Проверка и восстановление

Проверить SHA checkout, Compose state, `/readyz`, затем контролируемые direct reply,
callback edit и broadcast. Readiness не доказывает регистрацию webhook, доступность
публичного TLS proxy или доставку Kafka. Проверить backlog в backend и trace в Grafana.
Prometheus scrape: `telegram:9093/metrics` в prometheus-net; настройка scrape отдельная.

Ошибка readiness делает CD неуспешным, но не откатывает контейнеры/checkout.
Остановить приём, проверить безопасные логи (без токенов/updates), согласовать пару
версий. Нельзя откатывать один компонент за границу migration 004. Предпочтительно
исправление вперёд; restore БД требует backend runbook и учёта повторных отправок.
