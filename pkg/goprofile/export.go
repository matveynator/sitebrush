package goprofile

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/matveynator/sitebrush/v2/pkg/requesttiming"
)

const exportLimit = 120 << 10

type textReport struct {
	output    strings.Builder
	truncated bool
}

func (text *textReport) line(format string, arguments ...any) {
	if text.truncated {
		return
	}
	formatted := fmt.Sprintf(format, arguments...) + "\n"
	remaining := exportLimit - 128 - text.output.Len()
	if len(formatted) > remaining {
		formatted = formatted[:max(0, remaining)]
		for !utf8.ValidString(formatted) {
			formatted = formatted[:len(formatted)-1]
		}
		text.truncated = true
	}
	text.output.WriteString(formatted)
}
func (text *textReport) finish() string {
	if text.truncated {
		text.output.WriteString("\n[Отчёт сокращён по ограничению размера.]\n")
	}
	return text.output.String()
}
func roleLabel(role string) string {
	if role == "public" {
		return "Приём запросов и отдача файлов"
	}
	return "Обработка сайта и приложения"
}
func exportText(report Report) string {
	text := &textReport{}
	text.line("## %s", roleLabel(report.Role))
	text.line("Замер: %s; PID: %d; пользователь: %s", report.RunID, report.PID, report.User)
	text.line("Система: %s/%s; SiteBrush: %s; Go: %s", report.System, report.Architecture, report.Version, report.GoVersion)
	text.line("Запись: %s — %s", report.Started.Format("2006-01-02T15:04:05.999Z07:00"), report.CaptureFinished.Format("2006-01-02T15:04:05.999Z07:00"))
	text.line("CPU: %.3f мс; живая память Go: %d байт; выделено за запись: %d байт; сборок мусора: %d", float64(report.CPU)/1e6, report.Heap, report.Allocated, report.GC)
	text.line("HTTP на границе этого процесса: запросов %d; получено тел %d байт; отправлено тел %d байт; HTTP ошибок %d; ошибок записи %d", report.Network.Requests, report.Network.Received, report.Network.Sent, report.Network.Errors, report.Network.WriteErrors)
	text.line("HTTP-трафик приложения за прокси внутренний: его нельзя складывать с внешним трафиком.")
	text.line("Данные частичные: %t; ошибка записи: %s", report.Truncated, report.Error)
	text.line("CPU — выборка процессорной работы, не время ожидания. Образцы — не число вызовов. Живая память — выборка удерживаемой памяти; это не RSS процесса.")
	if report.RAM != nil {
		text.line("\n### Готовые RAM-ответы")
		text.line("Бюджет: %d байт; занято: %d байт; уникальных тел: %d; попаданий: %d; промахов: %d", report.RAM.Limit, report.RAM.Bytes, report.RAM.Bodies, report.RAM.Hits, report.RAM.Misses)
		for _, reason := range requesttiming.RAMMissReasons() {
			if count := report.RAM.MissReasons[reason]; count != 0 {
				text.line("RAM miss %s: %d", reason, count)
			}
		}
		for _, site := range report.RAM.Sites {
			text.line("RAM domain=%s routes=%d root_variants=%s root_miss=%s", site.Domain, site.Routes, strings.Join(site.RootVariants, ", "), site.RootMiss)
		}
		text.line("Бюджет включает подготовку и поколения с читателями; метаданные, временные буферы и RSS процесса считаются отдельно.")
	}
	if report.Runtime != nil {
		text.line("\n### Планировщик Go во время записи")
		text.line("Доступные runtime/metrics: %s", strings.Join(report.Runtime.Supported, ", "))
		for _, sample := range report.Runtime.Samples {
			text.line("t=%d мс; goroutine всего=%s running=%s runnable=%s waiting=%s syscall/cgo=%s; GOMAXPROCS=%s", sample.ElapsedMS, runtimeCount(sample, "/sched/goroutines:goroutines"), runtimeCount(sample, "/sched/goroutines/running:goroutines"), runtimeCount(sample, "/sched/goroutines/runnable:goroutines"), runtimeCount(sample, "/sched/goroutines/waiting:goroutines"), runtimeCount(sample, "/sched/goroutines/not-in-go:goroutines"), runtimeCount(sample, "/sched/gomaxprocs:threads"))
		}
		text.line("Ожидание планировщика: наблюдений %d; p95 <= %s мс; p99 <= %s мс", report.Runtime.Scheduler.Count, runtimeBound(report.Runtime.Scheduler.P95MS), runtimeBound(report.Runtime.Scheduler.P99MS))
		text.line("Goroutine-счётчики приблизительные и не обязаны складываться в общее число. Нет данных означает отсутствие метрики или конечной границы.")
	}
	text.line("\n### Задержки запросов")
	for _, stage := range report.Stages {
		text.line("%s: наблюдений %d; среднее %.3f мс; p90 <= %.3f мс; p95 <= %.3f мс; p99 <= %.3f мс", stage.Name, stage.Count, stage.MeanMS, stage.P90MS, stage.P95MS, stage.P99MS)
	}
	text.line("handler_complete — завершение обработчика; body_write — суммарное время вызовов записи тела; http1_response_complete — HTTP/1 keep-alive Active→Idle после финального flush net/http. Это серверные границы, а не подтверждение получения клиентом. HTTP/2 и оборванные соединения в последнюю стадию не входят.")
	functions := append([]Function(nil), report.Functions...)
	sortFunctions(functions, "self", false)
	text.functions("Функции по собственному CPU, нс", functions, 50)
	for _, function := range functions[:min(10, len(functions))] {
		detail := functionDetail(report.Functions, report.Calls, function.ID)
		text.line("\nСвязи %s (%s:%d)", function.Name, function.File, function.Line)
		text.functions("Вызывается из (затраты связи — Total)", detail.Callers, 5)
		text.functions("Вызывает (затраты связи — Total)", detail.Callees, 5)
	}
	sortFunctions(functions, "total", false)
	text.functions("Функции по CPU с вызовами, нс", functions, 50)
	text.functions("Строки кода по собственному CPU, нс", report.Code, 50)
	text.functions("Удерживаемая память по функциям, байт", report.Memory, 30)
	text.line("\n### Состояния goroutine: сгруппированные стеки")
	for _, stack := range groupedStacks(report.Goroutines) {
		text.line("%s", stack)
	}
	return text.finish()
}

func runtimeCount(sample RuntimeSample, name string) string {
	count, found := sample.Counts[name]
	if !found {
		return "нет данных"
	}
	return strconv.FormatUint(count, 10)
}

func runtimeBound(bound *float64) string {
	if bound == nil {
		return "нет данных"
	}
	return fmt.Sprintf("%.3f", *bound)
}
func (text *textReport) functions(title string, functions []Function, limit int) {
	text.line("\n### %s", title)
	text.line("Собственное | С вызовами | Образцы | Функция | Файл:строка")
	for _, function := range functions[:min(limit, len(functions))] {
		text.line("%d | %d | %d | %s | %s:%d", function.Self, function.Total, function.Samples, function.Name, function.File, function.Line)
	}
	if len(functions) > limit {
		text.line("[Показано %d из %d]", limit, len(functions))
	}
}

// Argument addresses are omitted: grouping preserves useful waiting locations without unstable pointer noise.
func groupedStacks(dump string) []string {
	counts := make(map[string]int)
	for _, block := range strings.Split(dump, "\n\n") {
		lines := strings.Split(block, "\n")
		if len(lines) < 3 || !strings.HasPrefix(lines[0], "goroutine ") {
			continue
		}
		state := "состояние неизвестно"
		if begin := strings.Index(lines[0], "["); begin >= 0 {
			state = strings.TrimSuffix(lines[0][begin:], ":")
		}
		var frames []string
		for index := 1; index+1 < len(lines) && len(frames) < 10; index += 2 {
			function := lines[index]
			if arguments := strings.LastIndex(function, "("); arguments >= 0 {
				function = function[:arguments]
			}
			location := strings.TrimSpace(lines[index+1])
			if offset := strings.Index(location, " +0x"); offset >= 0 {
				location = location[:offset]
			}
			frames = append(frames, function+" @ "+location)
		}
		counts[state+"\n"+strings.Join(frames, "\n")]++
	}
	type stackGroup struct {
		stack string
		count int
	}
	groups := make([]stackGroup, 0, len(counts))
	for stack, count := range counts {
		groups = append(groups, stackGroup{stack, count})
	}
	sort.Slice(groups, func(first, second int) bool {
		if groups[first].count == groups[second].count {
			return groups[first].stack < groups[second].stack
		}
		return groups[first].count > groups[second].count
	})
	result := make([]string, 0, min(10, len(groups)))
	for _, group := range groups[:min(10, len(groups))] {
		result = append(result, fmt.Sprintf("В захваченном снимке: %d goroutine\n%s", group.count, group.stack))
	}
	return result
}
