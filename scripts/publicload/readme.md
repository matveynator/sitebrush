# Измерение публичного HTTP/HTTPS

[Результаты контрольной серии](results.md).

`publicload` разделяет установление соединения и ожидание ответа. Проверяйте свой стенд: этот инструмент создаёт настоящую нагрузку.

## Режимы публичной выдачи файлов

`-public-static-mode=snapshot` включён по умолчанию. Режим выбирается при запуске и передаётся дочернему процессу; менять базу данных или формат JSON-политик для отката не требуется.

| Режим | Включённые изменения |
|---|---|
| `legacy` | Прежний путь, включая буфер HTTP/1.0 |
| `http10` | Прямая передача при известном Content-Length |
| `roots` | Дополнительно переиспользование безопасных корней опубликованных сайтов |
| `html` | Дополнительно подготовка bootstrap, места вставки и ETag при публикации |
| `snapshot` | Дополнительно чтение политик без очереди Catalog |

Например, запустите обычную установку с `-public-static-mode=http10`, затем с `-public-static-mode=roots`. Сохраняйте остальные параметры запуска и сравнивайте каждый следующий режим с предыдущим одним и тем же клиентом. Публикация подготавливает новое поколение корней и HTML до подтверждения обновления. Персональные страницы остаются в динамических обработчиках. Подготовленный HTML не содержит полной копии страницы в памяти; файлы продолжают читаться потоково.

Проверки выполняются вручную и не становятся обязательным нагрузочным этапом тестов или CI. Для быстрой диагностики HTTP/1.0 и HTTP/1.1 можно использовать:

```sh
ab -k -c 2000 -n 100000 -H 'Host: example.com' http://127.0.0.1/
wrk -t4 -c2000 -d30s -H 'Host: example.com' http://127.0.0.1/
```

Замените `example.com` настоящим опубликованным доменом. Запрос к `localhost/` проходит специальный маршрут. Сравнение разных клиентов служит диагностикой; вклад изменения измеряйте одним клиентом до и после. Учитывайте действующие блокировки и throttle: ответы 429 не считаются полезной пропускной способностью, защиту ради результата не отключайте.

Пользовательские проверки совместимости: GET/HEAD, HTTP/1.0 keep-alive, ETag/If-None-Match, Last-Modified/If-Modified-Since, Range/If-Range, языки и меню, пароль и Test Drive, смена публикации/импорт/откат, отзыв доступа, перезапуск административного процесса. Для производительности сравнивайте RPS, CPU, B/op, allocs/op и GC. Подтверждение цели 95% nginx требует отдельных результатов с одинаковыми ответами и условиями; приведённые изменения сами по себе её не доказывают.

Первое окно профилировщика сразу показывает полную статистику: таблицу процессов с PID, CPU, памятью и HTTP-счётчиками, функции с сортировкой, граф, строки кода, память, задержки, сеть и goroutine. Процессы подключаются автоматически и независимо; выбор процесса меняет просматриваемую таблицу, а запись охватывает все обнаруженные обслуживающие процессы. Сбой одного контакта помечает результат неполным и не скрывает доступные части. Запись по-прежнему запускается кнопкой.

Если при выборе публичного процесса профилировщик показывает `update supervisor NetChan dial: netchan: channel closed`, команды автоматически используют резервный контакт на существующем HTTP listener через `127.0.0.1`. Контакт требует секрет процесса и недоступен внешнему клиенту; переключение транспорта не запускает вторую запись с тем же RunID. На OpenBSD отдельно проверьте начало записи, получение отчёта и закрытие диалога. Автоматический запуск профилирования не добавлен.

```sh
CGO_ENABLED=0 go run ./scripts/publicload \
  -url https://site.example.test/ \
  -requests 10000 -concurrency 600 \
  -new-connections -tls-version 1.2 \
  -warmup 1000 -repeat 5 -bytes 8836
```

Замените адрес адресом своего стенда. Для локального сертификата передайте `-ca certificate.pem`; проверка сертификата остаётся включённой. `-sha256` дополнительно проверяет содержимое тела ответа.

## Режимы

| Что измеряем | Параметры |
|---|---|
| HTTP keep-alive | `-url http://…` |
| Новое TCP-соединение на запрос | `-url http://… -new-connections` |
| HTTPS keep-alive | `-url https://…` |
| Новый TLS handshake на запрос | `-url https://… -new-connections` |
| Повторное использование TLS-сессии | `-new-connections -tls-resume` |
| HTTP/2 streams | `-http2` |
| Синхронные волны новых соединений | `-new-connections -burst` |
| Подтверждённые блокировки/лимиты | `-allow-non-2xx` |

Новые соединения принудительно используют HTTP/1.1. HTTP/2 проверяется отдельно: число параллельных streams не равно числу TCP-соединений. Фактически выбранные протоколы и количество новых/повторно использованных соединений входят в отчёт.

## Как читать отчёт

| Поле | Значение |
|---|---|
| `DNS`, `TCP`, `TLS` | Отдельные интервалы клиентских событий; p50/p95/p99 в микросекундах |
| `ConnectionWait` | `GetConn → GotConn`: ожидание транспорта вместе с созданием соединения, если оно требуется |
| `ResponseWait` | Отправленный запрос → первый байт ответа |
| `TotalTTFB` | Начало запроса → первый байт; включает все предшествующие стадии |
| `SuccessfulTTFB` | То же, только для ответов, прошедших проверку статуса и тела |
| `SuccessfulPerSecond` | Полезный RPS; ошибки и ограничения не улучшают эту цифру |
| `Statuses`, `TransportErrors` | HTTP-статусы и отдельные транспортные ошибки |
| `LengthMismatches`, `ContentMismatches` | Несовпадение ожидаемого размера или SHA256 у успешного HTTP-ответа |
| `IncompleteTraces` | Потерянные события клиентских измерений |

`Samples: 0` означает отсутствие измерений этой стадии, например TLS у HTTP или установки соединения при keep-alive. Перцентили разных стадий нельзя складывать. Прогрев выполняется отдельно от измеренного интервала. При нескольких повторах выводится поток JSON-отчётов с номером `Run`.

## Внутренняя диагностика сервера

Включите `sitebrush -request-timings`. Флаг передаётся публичному и дочернему процессам. Каждые 30 секунд выводятся счётчики с `role=public` или `role=application`:

- domain/policy lookup, AttackGuard и ThrottleGuard;
- ожидание очереди и работа каждого security owner;
- certificate lookup, Front.State, proxy metadata и backend;
- вход в HTTP handler → первая запись ответа.

`accepted_to_request` — составной серверный интервал accept → HTTP handler, включающий TLS и получение запроса; его нельзя считать отдельным TLS handshake. Серверная первая запись и фактический первый байт у клиента — разные измерения.

Внутренние `p50_upper`, `p95_upper`, `p99_upper` — верхние границы гистограмм. Клиентские перцентили вычисляются из индивидуальных отсчётов. Диагностика по умолчанию выключена; её влияние проверяется отдельным сценарием `timings-off`.

Сборщик не задерживает посетителя при переполнении. `incomplete` и `dropped_minimum` обозначают неполноту и нижнюю границу потерь: объединённый сигнал переполнения не выдаётся за точный счётчик. Публичные observation batches также содержат `Incomplete` и `DroppedMinimum`, сохраняя принятый пакет до acknowledgement.

## Встроенные сценарии

```sh
CGO_ENABLED=0 go test . -run '^$' \
  -bench '^BenchmarkPublicConnections/https-new/clients=600/normal$' \
  -benchtime=10000x -count=5

CGO_ENABLED=0 go test . -run '^$' \
  -bench '^BenchmarkPublicIngressAllocations$' -benchmem -count=5
```

Матрица содержит 300/600/1000 клиентов, HTTP/HTTPS, HTTP/2, TLS 1.2, TLS resumption, обычные и доверенные запросы, разные IP, один security shard, полную очередь аналитики, отсутствующего consumer, блокировку, throttle и burst. Обычный HTTP/TLS стенд использует тот же публичный обработчик и certificate lookup, что настоящий listener. Синтетические IP подставляются только тестовым адаптером; production ingress тестовые заголовки не читает.

Размер проверяемого ответа — 8836 байт вместе с visitor bootstrap. Сетевой benchmark включает затраты генератора; allocations серверного обработчика измеряются отдельно, без HTTP-клиента и TLS.

На macOS длинная серия новых соединений может исчерпать исходящие TCP-порты и получить `can't assign requested address`. Такой прогон не подходит для сравнения производительности сервера. Для повторяемой серии используйте отдельный Linux-стенд или одинаковые локальные Linux-контейнеры; не смешивайте их результаты с macOS.

Строгость security проверяется отдельно от скорости. Подтверждённое ограничение возвращает 429; недоступное обязательное решение — 503 без обхода защиты. Заполненная очередь аналитики не меняет ответ посетителю. В режиме обновления лимиты принадлежат публичному процессу; при несовместимом старом child переход откладывается до согласования возможностей и перезапуска launcher.

## Prepared public responses

In `snapshot` mode, `--public-ram-cache-mib=512` prepares small published HTML,
static files and known public resources up to 256 KiB per final representation.
Publication records source fingerprints; preparation verifies them beneath the
site's `os.Root` before atomically installing immutable bodies. Legacy policies
without these optional fingerprints continue using the safe file path.

The budget covers unique payloads, reservations and generations still borrowed
by requests. It is not an RSS limit: Go metadata, collection, connections and
preparation scratch space are separate. Identical representations share bytes.
The split application does not allocate a second copy of the public RAM cache.
`--public-ram-cache-mib=0` disables admission and restores file delivery.

An ordinary cache hit uses prepared headers and one body write. Range and
conditional requests use `http.ServeContent` over shared bytes; no source file
is opened. Private, password, token, registration and personalized responses
keep their existing handlers. Oversized objects and cache misses retain safe
streaming. A stale or blocked generation fails closed rather than falling back.

Compare the same published route with budget `0` and `512`, then compare nginx
using identical response bodies and recorded configuration. Benchmark and
profiling runs remain manual and are not required CI stages.

## Response and scheduler diagnostics

The existing profiler now includes `handler_complete`, cumulative `body_write`
time and `http1_response_complete`, with p90/p95/p99 histogram upper bounds.
HTTP/1 completion uses the server's Active-to-Idle transition after its final
buffer flush. It covers keep-alive responses, excludes HTTP/2 and interrupted
connections, and does not confirm client receipt. No extra socket wrapper or
forced Flush changes platform file-transfer behavior. Lost observations discard
pending connection correlations and explicitly mark the report incomplete.

During a recording, runtime gauges are sampled once per second: goroutines,
running/runnable/waiting, syscall/cgo, GOMAXPROCS and scheduler latency. Missing
metrics remain unavailable; approximate gauges need not add up to the total.
The first screen includes RAM occupancy, budget, body count, hits and misses;
the Goroutine tab shows the runtime samples. The copied report includes both.
Diagnostic observation is present only with profiling or request timings enabled.
