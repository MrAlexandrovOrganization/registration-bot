# Registration Telegram frontend

## Версии сборки

Go и protoc-gen-go берутся из `go.mod`; образы и остальные инструменты —
из `versions.mk`; Python-инструменты — из `requirements-tools.txt`.
`make versions` показывает значения. Make экспортирует их в Compose, скрипты
проверок и CI. Сборка образа: `make compose-build`; запуск: `make up`.
После изменения инструментов повторите `make install`.

Go-frontend для регистрации: long polling, представление анкеты, получение
заданий из Kafka и вызовы Telegram API. Backend владеет всеми данными и правами;
здесь нет драйвера PostgreSQL, SQLite, копии анкеты или бизнес-правил валидации.

## Подготовка

Prerequisites: Go **1.26.1**, protoc **34.0**, Python 3 с venv, Make, Docker Compose v2.

```sh
make install
make install-hooks  # отдельно, неизвестные hooks не перезаписываются
make format
make check
make test-race
make build
```

Инструменты закреплены: protoc-gen-go, protoc-gen-go-grpc и
goimports в `.bin` (версии: `make versions`), pre-commit и Ruff в `.tools`,
Python-зависимости закреплены в requirements-tools.txt. `format` применяет
goimports, go fix и Ruff. `check` — формат, генерация, vet, fake-тесты и Compose
на синтетических значениях, Gitleaks из `versions.mk` с redaction для версионируемых файлов;
ни одного реального Telegram-запроса. Docker нужен для Compose и secret scan.
Generated `*.pb.go` игнорируются Git, как в notes-bot. `make build`, `test`,
`test-race`, `check`, `format` и `run` сначала выполняют `proto-gen`.
Для прямых Go-команд после checkout сначала выполнить `make proto-gen`.
Docker исключает локальный generated-код и сам генерирует его в build-stage
закреплёнными инструментами. Проверка генерации/компиляции заменяет сравнение
с закоммиченными файлами. Frontend по-прежнему собирается независимо.
Canonical source — backend `api/registration.proto`; местный файл является
snapshot контракта v1, меняется только согласованно с backend. `go_package`
переопределяется генератором, не ручной правкой generated-файлов.

CI на PR и push main вызывает те же Make-цели и сборку контейнера. Автодеплоя
пока нет. Публикация Git-репозиториев и первое переключение — отдельные операции.

## Запуск

Подготовить `.env` из `.env.example`: в нём только `BOT_TOKEN` и `BACKEND_TOKEN`.
Адреса, лимиты, Kafka и настройки наблюдаемости заданы явно в
`docker-compose.yml`; для изменения этих значений `.env` не используется.

Запуск требует подготовленного backend и топиков Kafka. Бот запускается командой
`make up`, останавливается через `make down`; дополнительный флаг включения не нужен.
ID бота вычисляется из числовой части `BOT_TOKEN` перед двоеточием;
проверка `getMe` сверяет с ним идентичность бота. Отдельный `BOT_ID` frontend не нужен.
При старте frontend получает sender lease; другая активная копия вызывает отказ.
При перезапуске lease предыдущего процесса может сохраняться до 90 секунд.

```sh
make up
make down
```

На VM используются external networks `registration-api`, `kafka-net`, `jaeger-net`
и `prometheus-net`. Backend доступен как `registration-backend:50051`, Kafka —
`kafka:9092`, OTLP gRPC — `http://jaeger:4317` (без TLS внутри Docker-сети),
как в `notes-bot`. PostgreSQL к frontend не подключена.

Для запуска вне Docker `make run` читает экспортированные переменные shell,
не `.env` и не Compose. Дополнительно к двум переменным из `.env` нужно задать
доступные с хоста `BACKEND_ADDR`, `KAFKA_BROKERS`, `HTTP_LISTEN=127.0.0.1:9091`,
и `ALLOW_INSECURE_GRPC=true` для доверенного внутреннего соединения
(либо `GRPC_CA_FILE` для TLS). Остальные переменные имеют значения по умолчанию;
трассировка вне Compose включается через `OTEL_EXPORTER_OTLP_ENDPOINT`.
Для host-разработки нужны доступные advertised Kafka listeners:
SSH tunnel к одному порту не исправляет advertised address `kafka`.

Справочник переменных приложения (не список полей `.env`):

| Переменная | Назначение |
|---|---|
| `BOT_TOKEN` | Telegram token; ID бота определяется автоматически из префикса токена |
| `TELEGRAM_LOCAL_API_URL` | Доверенный Bot API origin; в Compose http://telegram-bot-api:8081 через внешнюю telegram-net, пустое значение на хосте — публичный API |
| `BACKEND_TOKEN` | Общий секрет backend, минимум 32 символа |
| `BACKEND_ADDR` | gRPC endpoint |
| `GRPC_CA_FILE` | CA для TLS backend |
| `ALLOW_INSECURE_GRPC` | Явное разрешение plaintext только в доверенной сети одной VM |
| `KAFKA_BROKERS` | Через запятую; `kafka:9092` по умолчанию |
| `KAFKA_TOPIC_PREFIX` | `registration.telegram`, должен совпадать с backend |
| `KAFKA_GROUP` | `registration-telegram`, не использовать чужую группу |
| `TOTAL_RATE` | 20 по умолчанию, диапазон 1..25 |
| `BROADCAST_RATE` | 15 по умолчанию, строго меньше TOTAL_RATE |
| `HTTP_LISTEN` | `:9091`; Compose публикует только host loopback |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | В Compose `http://jaeger:4317`; вне Compose необязателен |
| `OTEL_EXPORTER_OTLP_INSECURE` | В Compose true: OTLP без TLS внутри `jaeger-net` |

Webhook не используется и не регистрируется автоматически. Не запускать
одновременно Python/Go pollers или две frontend-реплики для одного token.
Compose использует существующий local Telegram Bot API; он должен быть запущен
в local mode и подключён к telegram-net. URL задаётся без токена и API path;
запросы getMe/getUpdates и отправки используют один клиент. Host-порты не добавляются.

## Пользовательские сценарии

`/start` открывает анкету, backend выбирает первое missing/invalid поле.
Заполненные актуальные ответы повторно не спрашиваются. Затем итоговое
подтверждение; редактирование через inline-кнопки. `/cancel` выходит из текущего
действия. Callback защищён версией состояния; старые кнопки предлагают `/start`.
Принятый update подтверждается в Telegram только после backend commit; повторы
дедуплицируются PostgreSQL. Callback spinner закрывается после сохранения.

Тексты, вопросы и справка команд — `internal/resources/texts.json`; отображение —
типизированный View renderer. HTML компонуется пакетом `tgfmt`, все динамические
значения экранируются. Перед рабочим запуском заменить `/about`, `/bring` и
сведения о мероприятии на актуальные — старые даты не переносились.

## Административные команды

- `/my_permissions`, `/stats`, `/sources [СТРАНИЦА]`, `/export` (XLSX только в личном чате).
- `/grant_permission USER_ID ПРАВО`, `/revoke_permission USER_ID ПРАВО`.
  Права: admin, table_viewer, message_sender, staff. Выдать/отнять admin может root.
- В нужной группе: `/register_staff_chat`, `/register_counselor_chat`,
  `/register_superuser_chat`.
- `/sync_staff_chat`, `/sync_counselor_chat`: фоновая проверка **известных**
  пользователей пакетами по 20. Bot API не перечисляет всех участников группы.
  Бот должен иметь доступ к getChatMember; неопределённая ошибка не означает уход.
- `/broadcast АУДИТОРИЯ`: следующее сообщение используется как copyMessage source;
  поддерживаются текст и копируемые Telegram медиа. Затем предпросмотр.
- `/poll АУДИТОРИЯ`: предпросмотр опроса участия.
- `/send ID`, `/progress ID`, `/pause ID`, `/resume ID`, `/cancel_broadcast ID`, `/retry ID`.

Аудитории: `all`, `registered`, `incomplete`, `yes`, `maybe`, `staff`, `counselor`.
Список фиксируется при `/send`; preview count может измениться до подтверждения.
Ссылка на source message должна оставаться доступной боту. Защищённые и другие
неподдерживаемые Telegram сообщения копировать нельзя; ошибка останавливает рассылку.

## Отправка и ограничения

### Источники первых запусков

Размещайте ссылки `https://t.me/ИМЯ_БОТА?start=website`, `?start=channel` или
`?start=poster_qr` (последнюю можно закодировать в QR). Отдельная метка — отдельный
счётчик; ссылки с одинаковой меткой объединяются. Предварительно создавать их в
боте не требуется. Метки регистрозависимы: 1–64 символа `A-Z`, `a-z`, `0-9`, `_`, `-`.

Backend сохраняет первый `/start` в личном чате один раз на Telegram-пользователя.
Повторные запуски, другая метка, повторная доставка update и перезапуск сервисов
не меняют источник, время и счётчик. Первый запуск без
валидной метки навсегда относится к «Без метки». Клики без запуска Telegram не сообщает.

`/sources` показывает уникальных пользователей по меткам, по 20 источников на
страницу; далее `/sources 2`. Доступ — root или `table_viewer`, как для `/stats`.
В `/stats` и `/help` есть подсказка команды. Источники существовавших до внедрения
и импортированных пользователей помечены «Неизвестен до начала учёта» и не
приписываются новым ссылкам. Пользователи без `/start` после внедрения не входят
в счётчики, поэтому сумма может отличаться от общего числа в `/stats`.

XLSX содержит `first_start_source`, `first_start_status`, `first_start_at_utc`.
Статусы: `tagged` — метка, `direct` — без метки, `unknown` — исторический источник
неизвестен, `not_started` — ещё не было `/start`. У исторических записей время пустое.
Для включения требуется обновить оба сервиса и применить backend-миграцию 002;
startup сам миграции не запускает.

### Доставка сообщений

Два топика `<prefix>.interactive.v1` и `<prefix>.broadcast.v1`, отдельные consumer
groups. В приоритете интерактивные ответы. Один активный sender, общий limiter
20/сек, массовая отправка 15/сек, один чат 1/сек, группа 20/мин без bursts.
Это верхние ограничения, не обещанная скорость: HTTP latency тоже влияет.

Kafka payload — ID задания. Содержимое, право владения, срок lease и статус
выдаёт backend. После Telegram HTTP sender сохраняет результат через gRPC и
лишь затем подтверждает Kafka offset. При недоступном backend новые отправки
останавливаются. При потере результата PostgreSQL scheduler восстановит задание.

`429` сохраняет retry_after и общий cooldown через backend; повтор не расходует
failure budget. Временные ошибки ограниченно повторяются, blocked прекращаются,
content error приостанавливает рассылку. Отмена не отзывает уже отправленное.
Exactly-once Telegram невозможен: принятый Telegram запрос с потерянным ответом
может быть повторён. `sent` означает принятие Telegram, не прочтение человеком.

## Наблюдаемость

JSON stdout с UTC time/service/level и trace_id/span_id активного контекста.
Токен в URL, response descriptions, тела запросов и анкеты не логируются;
Telegram errors преобразуются в безопасные коды. В тестах проверены HTTP и
сетевые ошибки с синтетическим секретом.

HTTP `/livez` — процесс, `/readyz` — свежесть успешных polling/backend sender
проверок. `/metrics`: `registration_telegram_updates_total`,
`registration_telegram_delivery_total`, runtime metrics. Kafka lag контролируется
средствами общей Kafka; старейшее outbox-задание — метрикой backend.
Compose подключает frontend к `jaeger-net` и `prometheus-net`, как `notes-bot`.
Для Prometheus настроить scrape `/metrics` на порту 9091; наличие сети само по себе
не добавляет scrape target. Проверить события в реальном Grafana отдельно после доставки.
