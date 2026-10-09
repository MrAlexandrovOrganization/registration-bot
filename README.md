# Registration Telegram frontend

## Версии сборки

Go и protoc-gen-go берутся из `go.mod`; образы и остальные инструменты —
из `versions.mk`; Python-инструменты — из `requirements-tools.txt`.
`make versions` показывает значения. Make экспортирует их в Compose, скрипты
проверок и CI. Сборка образа: `make compose-build`; запуск: `make up`.
После изменения инструментов повторите `make install`.

Go-frontend для регистрации: webhook или long polling, представление анкеты,
интерактивная доставка через gRPC и рассылки через Kafka. Backend владеет всеми данными и правами;
здесь нет драйвера PostgreSQL, SQLite, копии анкеты или бизнес-правил валидации.

## Подготовка

Prerequisites: Go (версия из `go.mod`), Python 3 с venv, Make, Docker Compose v2.

```sh
make install
make install-hooks  # отдельно, неизвестные hooks не перезаписываются
make format
make check
make test-race
make build
```

Инструменты закреплены: Buf, protoc-gen-go, protoc-gen-go-grpc и
goimports в `.bin` (версии: `make versions`), pre-commit и Ruff в `.tools`,
Python-зависимости закреплены в requirements-tools.txt. `format` применяет
goimports, go fix и Ruff. `check` — формат, проверка схемы, генерация, vet, fake-тесты и Compose
на синтетических значениях, Gitleaks из `versions.mk` с redaction для версионируемых файлов;
ни одного реального Telegram-запроса. Docker нужен для Compose и secret scan.
Generated `*.pb.go` игнорируются Git, как в notes-bot. `make build`, `test`,
`test-race`, `check`, `format` и `run` сначала выполняют `proto-gen`.
Для прямых Go-команд после checkout сначала выполнить `make proto-gen`.
Docker исключает локальный generated-код и сам генерирует его в build-stage
закреплёнными инструментами. Проверка генерации/компиляции заменяет сравнение
с закоммиченными файлами. Frontend по-прежнему собирается независимо.

### Protobuf / Buf

```sh
make install-proto  # только Buf и два Go-плагина в .bin
make proto-check    # buf build: проверка компиляции схемы, также входит в check
make proto-gen      # buf generate: генерация api/*.pb.go
```

Buf использует конфигурацию v2 (`buf.yaml`, `buf.gen.yaml`) и локальные плагины.
Отдельный protoc не нужен. Buf и protoc-gen-go-grpc закреплены в `versions.mk`,
protoc-gen-go берётся из версии protobuf в `go.mod`; `make versions` показывает
значения. После обновления версий выполните `make install-proto`.
Локальная сборка, CI и Docker используют одни Make-цели и версии.
Генерация не использует BSR, remote plugins или соседний checkout;
доступ к Go-модулям нужен при установке инструментов и зависимостей.

Канонический источник — `backends/registration/api/registration.proto` в backend.
Местный `api/registration.proto` — версионируемый побайтовый snapshot контракта v1.
Обновляйте его явно вручную из согласованной ревизии backend, после проверки
совместимости обоих сервисов; затем запускайте `make proto-check`, `make build`
и `make test-race`. Сборка сама snapshot не синхронизирует.
Не меняйте `go_package` в snapshot: `buf.gen.yaml` задаёт
`Mapi/registration.proto=registration.local/frontend/api` для **обоих** плагинов
и `paths=source_relative`. Generated-файлы не редактируются и не коммитятся.
`proto-check` проверяет компиляцию схемы, а не breaking changes или равенство
snapshot backend. Даже отдельная проверка breaking changes не доказывает
совместимость семантики RPC и порядка доставки — это требует согласования и тестов.

CI на PR и push main вызывает `make install`, `make check`, `make test-race`,
`make build`; отдельный job выполняет `make compose-build`. SSH CD зависит от обоих
jobs и включается repository variable `DEPLOY_ENABLED=true` только для push main.
По умолчанию деплой пропущен. Репозитории пока не опубликованы; provisioning и
coordinated rollout migration 004 описаны в [OPERATIONS.md](docs/OPERATIONS.md).
Все jobs используют `ubuntu-24.04`. Deploy использует environment `production`,
обновляет main fast-forward до проверенного SHA
и вызывает `make up`, как при локальном запуске. Эта цель собирает образ и ждёт
готовности по healthcheck до 180 секунд (`--wait --wait-timeout 180`).

## Запуск

Подготовить `.env` из `.env.example`: `BOT_TOKEN`, `BACKEND_TOKEN` и отдельный
`TELEGRAM_WEBHOOK_SECRET` для webhook-режима (в polling не используется).
`WEBHOOK_URL` в `.env` включает webhook (пустое значение — polling).
Остальные адреса, лимиты, Kafka и наблюдаемость заданы явно в `docker-compose.yml`.

Backend обязателен; топик Kafka нужен для рассылок. Интерактивные ответы работают
и при недоступной Kafka. Бот запускается командой
`make up`, останавливается через `make down`; дополнительный флаг включения не нужен.
ID бота вычисляется из числовой части `BOT_TOKEN` перед двоеточием;
проверка `getMe` сверяет с ним идентичность бота. Отдельный `BOT_ID` frontend не нужен.
При старте frontend получает sender lease; другая активная копия вызывает отказ.
При штатной остановке frontend дожидается завершения sender, heartbeat и Complete,
затем вызывает `ReleaseSender` с отдельным таймаутом 5 секунд. Backend освобождает
только lease этого worker, сохраняя cooldown и leases заданий. После аварии, ошибки
освобождения или остановки старой версии lease может сохраняться до 90 секунд.
Сначала обновить backend с поддержкой RPC, затем frontend; первое обновление старого
frontend ещё может ждать TTL, последующие штатные перезапуски освобождают lease.
Lease привязан к случайному ID процесса (`worker`), а не к изменению токена или
адреса backend. Frontend ждёт его освобождения внутри процесса: повторяет Claim
через 2 секунды при занятом lease или временной недоступности backend, не более
120 секунд суммарно (каждый RPC — до 10 секунд). До успешного Claim HTTP и приём
updates не запускаются. Логи различают занятый lease и недоступность backend;
остальные ошибки завершают запуск сразу с безопасным gRPC-кодом.

```sh
make up
make down
```

На VM используются external networks `registration-api`, `kafka-net`, `jaeger-net`
и `prometheus-net`. Backend доступен как `registration-backend:50052`, Kafka —
`kafka:9092`, OTLP gRPC — `http://jaeger:4317` (без TLS внутри Docker-сети),
как в `notes-bot`. PostgreSQL к frontend не подключена.

Для запуска вне Docker `make run` читает экспортированные переменные shell,
не `.env` и не Compose. Дополнительно к секретам из `.env` нужно задать
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
| `KAFKA_TOPIC_PREFIX` | `registration.telegram`, должен совпадать с backend; используется только `.broadcast.v1` |
| `KAFKA_GROUP` | `registration-telegram`, не использовать чужую группу |
| `TOTAL_RATE` | 20 по умолчанию, диапазон 1..25 |
| `BROADCAST_RATE` | 15 по умолчанию, строго меньше TOTAL_RATE |
| `HTTP_LISTEN` | `:9091`; Compose публикует только host loopback |
| `WEBHOOK_URL` | Пусто — polling; HTTPS URL — webhook. Без credentials, query и fragment |
| `TELEGRAM_WEBHOOK_SECRET` | Обязателен для webhook: 1–256 символов `A-Z a-z 0-9 _ -`; отдельный секрет заголовка Telegram |
| `WEBHOOK_LISTEN_ADDR` | `:8080`; отдельный HTTP listener webhook за TLS reverse proxy, точный путь из `WEBHOOK_URL` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | В Compose `http://jaeger:4317`; вне Compose необязателен |
| `OTEL_EXPORTER_OTLP_INSECURE` | В Compose true: OTLP без TLS внутри `jaeger-net` |

Compose использует существующий local Telegram Bot API; он должен быть запущен
в local mode и подключён к telegram-net. URL задаётся без токена и API path;
запросы getMe/getUpdates, отправки и явные команды управления webhook используют
один настроенный API origin.

### Режим получения обновлений

При пустом `WEBHOOK_URL` работает polling. При заданном URL запускается отдельный
webhook listener. Оба режима проверяют `getMe` и совпадение ID с token; одновременно
Python/Go pollers или две frontend-реплики для одного token запускать нельзя.

Webhook принимает только POST с `X-Telegram-Bot-Api-Secret-Token`; сравнение SHA-256
секрета выполняется constant-time. Тело ограничено 1 MiB, допускается ровно один JSON
update, очередь — 100 элементов, один consumer. Не более 101 запроса одновременно
читают тело/ожидают сохранения. Перегрузка, отмена и ошибка backend дают HTTP 503,
неуспешная авторизация — 401, неверное тело — 400/413. HTTP 200 для обрабатываемого
update возвращается **после durable Accept**, не после постановки в память и не после
отправки ответа пользователю. Неподдерживаемые виды update игнорируются без эффектов.
Ожидание в очереди и обработка ограничены 20 секундами; shutdown отменяет ожидания
и активный RPC, Telegram может безопасно повторить неподтверждённый update.
Порядок очереди — порядок поступления; backend дедуплицирует повторы и проверяет
версию callback. Регистрация использует `max_connections=1` для последовательной доставки.

Startup/shutdown не вызывают setWebhook/deleteWebhook. Отдельные команды бинарника
с настроенным окружением: `.bin/telegram register-webhook` и
`.bin/telegram delete-webhook`; обе сохраняют pending updates. Перед переходом на
polling webhook нужно явно удалить. Обычные тесты проверяют эти команды только
на fake Telegram. HTTP health/metrics остаётся на `HTTP_LISTEN`, не на webhook listener.

Для Compose: `make register-webhook` / `make delete-webhook` запускают готовый CLI
в одноразовом контейнере без запуска frontend или зависимостей. Перед этим собрать
актуальный образ через `make compose-build`. Это реальные Telegram-операции.
Webhook опубликован на `127.0.0.1:9083` → контейнер `8080`; TLS reverse proxy на хосте
должен передавать точный путь из `WEBHOOK_URL` и секретный заголовок без логирования.
Health/metrics Compose: `127.0.0.1:9093`. Порядок переключения — в OPERATIONS.

## Пользовательские сценарии

`/start` отправляет приветствие, затем сразу открывает анкету: backend выбирает
первое missing/invalid поле (для нового пользователя — ФИО).
Заполненные актуальные ответы повторно не спрашиваются. Затем итоговое
подтверждение; редактирование через inline-кнопки. `/cancel` выходит из текущего
действия. Callback защищён версией состояния; старые кнопки предлагают `/start`.
После заполнения последнего недостающего поля бот отправляет отдельное
сообщение `registration_completed` без кнопок, закрепляет его и затем отправляет
анкету с кнопками «Подтвердить анкету», «Изменить данные», «О выезде» и «Что взять?».
Итоговое подтверждение остаётся отдельным шагом; подтверждение и редактирование
не отправляют памятку повторно. Если `/start` снова обнаружил недостающие или
невалидные поля, после их заполнения отправляется и закрепляется новая памятка.
В подтверждённой анкете остаются данные и подсказка
редактирования; сведения о сборе и подтверждении участия доступны в `/about`.
Закреплённая памятка
не используется для навигации и не заменяется при редактировании анкеты.
Отправка и закрепление — две сохраняемые стадии одного задания: после сохранения
Telegram message ID повторяется только закрепление. Временная ошибка закрепления
задерживает анкету; при окончательной ошибке закрепления анкета всё равно доставляется.
Повторная доставка того же update не создаёт нового задания;
`/start` открывает сохранённую анкету без повторного закрепления.
Для этого сценария сначала обновить frontend (поддержка delivery kinds
`registration_completed` и `pin`), затем backend; схема БД и protobuf не меняются.
Принятый update подтверждается в Telegram только после backend commit; повторы
дедуплицируются PostgreSQL. Callback spinner закрывается после сохранения.
`Receipt.delivery_ids` передаются sender даже при `duplicate=true`: предыдущий
ответ Accept мог потеряться. Ожидания отправки перед подтверждением update нет.

Подходящие callback-переходы редактируют исходное сообщение. Frontend передаёт
`callback_message_editable` только для доступного текстового сообщения этого бота
в этом чате; inline/inaccessible/media и сообщения пользователя не подходят.
Backend выбирает `Delivery.edit_message_id` для допустимого private view.
При изменении телефона бот отправляет один новый вопрос с reply-кнопками
«Поделиться своим контактом» и «Отмена». Отдельной подписи к клавиатуре нет.
Telegram не позволяет прикрепить reply keyboard к editMessageText, поэтому
вопрос телефона всегда отправляется новым сообщением. Frontend переводит текст
reply-кнопки «Отмена» в личном чате в существующую команду `/cancel` для backend;
пользователю вводить команду не нужно. При ошибке ввода кнопки сохраняются.
При отмене изменения телефона бот
обновляет анкету и отправляет «Изменение отменено» с `remove_keyboard`, чтобы
скрыть кнопку отправки контакта. Обычный запрос телефона с reply keyboard,
уведомления, экспорт и рассылки остаются новыми сообщениями.
Редактирование использует тот же HTML renderer и inline markup. «Message is not
modified» — успешная доставка; fallback в новое сообщение разрешён только для
«message to edit not found»/«message can't be edited» и повторно проходит общий
limiter. Сетевые/неоднозначные ошибки не вызывают немедленной второй отправки.

Итоговая анкета содержит кнопки «О выезде» и «Что взять?».
Информационные кнопки работают и после продолжения анкеты, не изменяя её состояние;
на информационном экране есть кнопка возврата к текущему вопросу/анкете.

Публичная `/help` (`help_public`) содержит `/start`, `/cancel`, `/about`, `/bring`,
`/help`, без административных команд. Отдельного интерфейса участника нет.
Недоступные административные и неизвестные команды молча завершаются в backend:
нет outbox-ответа, отказа или подсказки. Выбор операторской справки и проверка доступа
при приёме команды и выдаче queued ответа — backend. Отменённый ответ возвращается
из Claim как `id=0`, frontend ничего не отправляет. Ошибки анкеты, некорректное
содержимое сообщения (`invalid_content`) и обычная информация продолжают отображаться.

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
не меняют уже записанные источник и время. У новых пользователей первый запуск без
валидной метки навсегда относится к «Без метки». Клики без запуска Telegram не сообщает.

`/sources` показывает уникальных пользователей по меткам, по 20 источников на
страницу; далее `/sources 2`. Доступ — root или `table_viewer`, как для `/stats`.
В `/stats` и `/help` есть подсказка команды. Источники существовавших до внедрения
и импортированных пользователей помечены «Неизвестен до начала учёта», пока
не получен первый запуск с валидной меткой. Обычный `/start` или невалидная метка
оставляют их источник и время пустыми. Для рассылки старым участникам используйте,
например, `https://t.me/ИМЯ_БОТА?start=old_users_2026`: первый запуск сохранит метку
и время возвращения, последующие их не изменят. Это учёт возвращений, а не
подтверждение участия; пересланной ссылкой может воспользоваться и новый человек.
Пользователи без записи в `first_starts` и без `/start` не входят
в счётчики, поэтому сумма может отличаться от общего числа в `/stats`.

XLSX содержит `first_start_source`, `first_start_status`, `first_start_at_utc`.
Статусы: `tagged` — метка, `direct` — без метки, `unknown` — исторический источник
неизвестен, `not_started` — ещё не было `/start`. У записей со статусом `unknown` время пустое.
Для включения требуется обновить оба сервиса: сначала backend `make up` (применяет
миграции, включая 002), затем frontend `make up`. Frontend миграции не запускает;
порядок согласованного переключения — в [OPERATIONS.md](docs/OPERATIONS.md).

### Доставка сообщений

Kafka используется только для `<prefix>.broadcast.v1`, группа `<group>-broadcast`.
Интерактивные ответы поступают непосредственно из Accept и через периодический
`PendingInteractive(limit=100)` при старте и далее каждую секунду (при ошибках RPC
backoff до 15 секунд). Нет cursor: повторное discovery находит due retries,
expired leases, exports, sync continuations и milestones независимо от Kafka.
Fast-path и recovery имеют по 100 мест и общую дедупликацию queued/in-flight ID;
переполнение отбрасывает только подсказки, durable задания остаются в backend.
Sender выбирает fast-path, recovery и broadcast без голодающей очереди.

Один активный sender и worker identity, общий Claim/Complete и limiter:
20/сек, массовая отправка 15/сек, один чат 1/сек, группа 20/мин без bursts.
Это верхние ограничения, не обещанная скорость: HTTP latency тоже влияет.

Kafka payload — ID задания. Содержимое, право владения, срок lease и статус
выдаёт backend. После Telegram HTTP sender сохраняет результат через gRPC с новым
ограниченным reporting context; для broadcast лишь затем подтверждает Kafka offset.
Fetch, повторы и commit Kafka выполняются вне sender и не блокируют direct/recovery.
При недоступном backend новые отправки
останавливаются. При потере результата PostgreSQL scheduler восстановит задание.

`429` сохраняет retry_after и общий cooldown через backend; повтор не расходует
failure budget. Временные ошибки ограниченно повторяются, blocked прекращаются,
content error приостанавливает рассылку. Отмена не отзывает уже отправленное.
Exactly-once Telegram невозможен: принятый Telegram запрос с потерянным ответом
может быть повторён. `sent` означает принятие Telegram, не прочтение человеком.

Переход требует согласованного backend с миграцией 004 и нового frontend: старый
frontend не читает delivery IDs, а новый backend больше не публикует interactive
в Kafka. Proto snapshot здесь побайтово соответствует canonical backend; сборка
и тесты не требуют соседнего checkout. Статусы/leases старых заданий не сбрасываются.

## Наблюдаемость

`make benchmark` измеряет webhook ingress и sender на fake backend/Telegram.
Сценарии, ограничения и результаты — [PERFORMANCE.md](docs/PERFORMANCE.md).

JSON stdout с UTC time/service/level и trace_id/span_id активного контекста.
Токен в URL, response descriptions, тела запросов и анкеты не логируются;
Telegram errors преобразуются в безопасные коды. В тестах проверены HTTP и
сетевые ошибки с синтетическим секретом.

HTTP `/livez` — процесс, `/readyz` — свежесть успешных backend sender проверок и,
в polling-режиме, Telegram polling. В webhook-режиме отсутствие входящих запросов
не делает сервис неготовым; Kafka не является зависимостью готовности interactive.
`/metrics`: `registration_telegram_updates_total`,
`registration_telegram_delivery_total`, runtime metrics. Kafka lag контролируется
средствами общей Kafka; старейшее outbox-задание — метрикой backend.
Compose подключает frontend к `jaeger-net` и `prometheus-net`, как `notes-bot`.
Для Prometheus настроить scrape `/metrics` на порту 9091; наличие сети само по себе
не добавляет scrape target. Проверить события в реальном Grafana отдельно после доставки.
