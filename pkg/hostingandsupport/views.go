package hostingandsupport

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

// View models live in the hosting package because the section is a control plane,
// while sitebrush.go only wires HTTP requests and application-specific callbacks.
type OverviewView struct {
	HealthTitle      string
	HealthText       string
	HealthClass      string
	ProblemCount     int
	PendingRequests  int
	UnpaidInvoices   int
	OverLimitSites   int
	StaleServers     int
	MailErrors       int
	ClientCount      int
	SiteCount        int
	ServerCount      int
	LastSyncLabel    string
	PaymentSetupText string
	Actions          []OverviewAction
}

type OverviewAction struct {
	Title      string
	Text       string
	ButtonText string
	Tab        string
	Level      string
}

type ServerView struct {
	ID                     string
	Name                   string
	Subtitle               string
	Local                  bool
	OwnerEmail             string
	SiteCount              int
	ClientCount            int
	InvoiceCount           int
	BillableCount          int
	UnpaidInvoiceCount     int
	TotalUsedLabel         string
	DiskFreeLabel          string
	DiskTotalLabel         string
	SyncStatusLabel        string
	SyncStatusClass        string
	NetworkStatusLabel     string
	NetworkStatusClass     string
	InvoiceActionLabel     string
	InvoiceActionClass     string
	DefaultInvoiceClient   string
	DefaultInvoiceDomain   string
	DefaultInvoicePlan     string
	DefaultInvoiceAmount   string
	DefaultInvoiceCurrency string
	SystemMetrics          []ServerMetricView
	Sites                  []ServerSiteView
	Clients                []ServerClientView
	Invoices               []ServerInvoiceView
	Plans                  []ClientHostingPlan
	Settings               []ServerSettingView
	Diagnostics            []ServerDiagnosticView
}

type ServerSiteView struct {
	Domain            string
	URL               string
	OwnerEmail        string
	AdminEmails       string
	PlanName          string
	PaidStatus        string
	UsedLabel         string
	LimitLabel        string
	OverLimit         bool
	InvoiceLabel      string
	BillingPriceLabel string
	BillingStatusText string
	BillingAmount     string
	BillingCurrency   string
	BillingBillable   bool
}

type ServerClientView struct {
	Email     string
	SiteCount int
	Domains   string
	Sites     []ServerSiteView
}

type ServerInvoiceView struct {
	Number         string
	CustomerEmail  string
	Domain         string
	PlanName       string
	AmountLabel    string
	StatusLabel    string
	PaymentURL     string
	PeriodLabel    string
	HistoryLabel   string
	RecurringLabel string
	CanPay         bool
}

type ServerMetricView struct {
	Name        string
	Value       string
	StatusClass string
}

type ServerSettingView struct {
	Name  string
	Value string
}

type ServerDiagnosticView struct {
	Name        string
	Value       string
	StatusClass string
}

type LocalServerViewInput struct {
	Sites         []Site
	Invoices      []Invoice
	Plans         []Plan
	Assignments   map[string]ServiceAssignment
	SystemMetrics []ServerMetricView
	MainDomain    string
	CurrentHost   string
	SiteURL       func(string) string
}

func BuildOverview(sites []Site, clientCount int, siteRequests []SiteRequest, invoices []Invoice, hostings []ClientHosting, syncEvents []RegistrySyncEvent, serviceMailEvents []ServiceMailEvent) OverviewView {
	view := OverviewView{
		PendingRequests:  len(siteRequests),
		ClientCount:      clientCount,
		SiteCount:        len(sites),
		ServerCount:      len(hostings),
		LastSyncLabel:    "синхронизаций ещё нет",
		PaymentSetupText: "стоимость считается по занятым мегабайтам",
	}
	if len(syncEvents) > 0 {
		view.LastSyncLabel = firstNonEmpty(syncEvents[0].CreatedAt, "синхронизация была, дата не передана")
	}
	for _, site := range sites {
		if site.UsedPercent >= 100 {
			view.OverLimitSites++
		}
	}
	now := time.Now().UTC()
	for _, hosting := range hostings {
		if HostingSyncIsStale(hosting.LastSeenAt, now) {
			view.StaleServers++
		}
	}
	for _, invoice := range invoices {
		switch strings.TrimSpace(invoice.Status) {
		case "issued", "payment_error":
			view.UnpaidInvoices++
		}
	}
	for _, event := range serviceMailEvents {
		if strings.TrimSpace(event.Error) != "" || strings.TrimSpace(event.Status) == "error" {
			view.MailErrors++
		}
	}
	view.ProblemCount = view.PendingRequests + view.UnpaidInvoices + view.OverLimitSites + view.StaleServers + view.MailErrors
	if view.ProblemCount == 0 {
		view.HealthTitle = "Всё спокойно"
		view.HealthText = "Заявок, неоплаченных записей и критичных проблем сейчас нет."
		view.HealthClass = "hosting-overview-ok"
	} else {
		view.HealthTitle = strconv.Itoa(view.ProblemCount) + " требует внимания"
		view.HealthText = "Сначала разберите эти пункты, остальное можно смотреть позже."
		view.HealthClass = "hosting-overview-warning"
	}
	view.Actions = overviewActions(view)
	return view
}

func BuildServerViews(localServer ServerView, clientHostings []ClientHosting, invoices []Invoice) []ServerView {
	servers := make([]ServerView, 0, len(clientHostings)+1)
	servers = append(servers, localServer)
	for _, clientHosting := range clientHostings {
		servers = append(servers, BuildRemoteServerView(clientHosting, invoices))
	}
	sort.SliceStable(servers[1:], func(left, right int) bool {
		leftServer := servers[left+1]
		rightServer := servers[right+1]
		if leftServer.SiteCount != rightServer.SiteCount {
			return leftServer.SiteCount > rightServer.SiteCount
		}
		return leftServer.Name < rightServer.Name
	})
	return servers
}

func BuildLocalServerView(input LocalServerViewInput) ServerView {
	serverName := firstNonEmpty(normalizeDomainName(input.MainDomain), normalizeDomainName(input.CurrentHost), "SiteBrush.com")
	server := ServerView{
		ID:                 "local",
		Name:               serverName,
		Subtitle:           "локальный сервер SiteBrush",
		Local:              true,
		SyncStatusLabel:    "локальные данные",
		SyncStatusClass:    "billing-sync-ok",
		NetworkStatusLabel: "проверяется фоновым мониторингом",
		NetworkStatusClass: "hosting-metric-ok",
		SystemMetrics:      input.SystemMetrics,
	}
	if len(server.SystemMetrics) == 0 {
		server.SystemMetrics = []ServerMetricView{
			{Name: "Статус", Value: "локальные данные", StatusClass: "hosting-metric-ok"},
		}
	}
	for _, siteRow := range input.Sites {
		ownerEmail := firstHostingSnapshotEmail(splitEmailList(siteRow.AdminEmails))
		if ownerEmail == "" {
			ownerEmail = "owner not set"
		}
		siteURL := siteRow.URL
		if input.SiteURL != nil {
			siteURL = input.SiteURL(siteRow.Domain)
		}
		server.Sites = append(server.Sites, ServerSiteView{
			Domain:            siteRow.Domain,
			URL:               siteURL,
			OwnerEmail:        ownerEmail,
			AdminEmails:       siteRow.AdminEmails,
			PlanName:          "Дисковое пространство",
			PaidStatus:        siteRow.BillingStatusText,
			UsedLabel:         siteRow.UsedLabel,
			LimitLabel:        siteRow.LimitLabel,
			OverLimit:         siteRow.UsedPercent >= 100,
			InvoiceLabel:      InvoiceLabelForDomain(input.Invoices, siteRow.Domain),
			BillingPriceLabel: siteRow.BillingPriceLabel,
			BillingStatusText: siteRow.BillingStatusText,
			BillingAmount:     siteRow.BillingAmount,
			BillingCurrency:   siteRow.BillingCurrency,
			BillingBillable:   siteRow.BillingBillable,
		})
	}
	server.SiteCount = len(server.Sites)
	server.Clients = serverClientViewsFromSites(server.Sites)
	server.ClientCount = len(server.Clients)
	server.Invoices = ServerInvoiceViews(input.Invoices, serverDomains(server.Sites), nil)
	server.InvoiceCount = len(server.Invoices)
	server.BillableCount = BillableSiteCount(server.Sites)
	server.UnpaidInvoiceCount = UnpaidInvoiceCount(server.Invoices)
	server.InvoiceActionLabel, server.InvoiceActionClass = InvoiceAction(server.BillableCount, server.UnpaidInvoiceCount)
	server.TotalUsedLabel = serverTotalUsedLabel(server.Sites)
	applyServerInvoiceDefaults(&server)
	return server
}

func BuildRemoteServerView(clientHosting ClientHosting, invoices []Invoice) ServerView {
	server := ServerView{
		ID:                 strings.TrimSpace(clientHosting.InstallationID),
		Name:               firstNonEmpty(clientHosting.ServerDomain, clientHosting.InstallationID),
		Subtitle:           firstNonEmpty(clientHosting.ServerStatus, clientHosting.ServerIP, "удалённый сервер SiteBrush"),
		OwnerEmail:         clientHosting.OwnerEmail,
		SiteCount:          clientHosting.SiteCount,
		TotalUsedLabel:     clientHosting.TotalUsedLabel,
		DiskFreeLabel:      clientHosting.DiskFreeLabel,
		DiskTotalLabel:     clientHosting.DiskTotalLabel,
		NetworkStatusLabel: clientHosting.NetworkUptimeLabel,
		NetworkStatusClass: clientHosting.NetworkStatusClass,
		SystemMetrics:      ServerSystemMetricViews(clientHosting),
	}
	stale := HostingSyncIsStale(clientHosting.LastSeenAt, time.Now().UTC())
	if strings.TrimSpace(clientHosting.LastSeenAt) == "" {
		server.SyncStatusLabel = "нет синхронизации"
		server.SyncStatusClass = "billing-sync-stale"
	} else if stale {
		server.SyncStatusLabel = "устарело · " + clientHosting.LastSeenAt
		server.SyncStatusClass = "billing-sync-stale"
	} else {
		server.SyncStatusLabel = "синхронизировано · " + clientHosting.LastSeenAt
		server.SyncStatusClass = "billing-sync-ok"
	}
	for _, site := range clientHosting.Sites {
		ownerEmail := firstNonEmpty(site.OwnerEmail, firstHostingSnapshotEmail(site.AdminEmails), clientHosting.OwnerEmail, "owner not set")
		billingPrice := BillingPriceForUsedBytes(site.UsedBytes)
		server.Sites = append(server.Sites, ServerSiteView{
			Domain:            site.Domain,
			URL:               "http://" + site.Domain + "/",
			OwnerEmail:        ownerEmail,
			AdminEmails:       strings.Join(site.AdminEmails, ", "),
			PlanName:          "Дисковое пространство",
			PaidStatus:        billingPrice.StatusText,
			UsedLabel:         site.UsedLabel,
			LimitLabel:        site.LimitLabel,
			OverLimit:         site.OverLimit,
			InvoiceLabel:      InvoiceLabelForDomain(invoices, site.Domain),
			BillingPriceLabel: billingPrice.PriceLabel,
			BillingStatusText: billingPrice.StatusText,
			BillingAmount:     billingPrice.Amount,
			BillingCurrency:   billingPrice.Currency,
			BillingBillable:   billingPrice.Billable,
		})
	}
	server.Clients = serverClientViewsFromSites(server.Sites)
	server.ClientCount = len(server.Clients)
	server.Invoices = ServerInvoiceViews(invoices, serverDomains(server.Sites), serverClientEmails(server.Clients))
	server.InvoiceCount = len(server.Invoices)
	server.BillableCount = BillableSiteCount(server.Sites)
	server.UnpaidInvoiceCount = UnpaidInvoiceCount(server.Invoices)
	server.InvoiceActionLabel, server.InvoiceActionClass = InvoiceAction(server.BillableCount, server.UnpaidInvoiceCount)
	applyServerInvoiceDefaults(&server)
	return server
}

func ServerSystemMetricViews(clientHosting ClientHosting) []ServerMetricView {
	osLabel := strings.TrimSpace(clientHosting.OSName + " " + clientHosting.OSVersion)
	cpuLabel := fmt.Sprintf("%s · %d ядер", firstNonEmpty(clientHosting.CPUModel, "CPU не передан"), clientHosting.CPUCores)
	diskLabel := clientHosting.DiskUsedLabel + " занято / " + clientHosting.DiskFreeLabel + " свободно / " + clientHosting.DiskTotalLabel + " всего"
	return []ServerMetricView{
		{Name: "OS", Value: firstNonEmpty(osLabel, "ОС не передана"), StatusClass: "hosting-metric-ok"},
		{Name: "CPU", Value: cpuLabel, StatusClass: clientHosting.CPUStatusClass},
		{Name: "CPU %", Value: fmt.Sprintf("%.1f%%", clientHosting.CPUUsagePercent), StatusClass: clientHosting.CPUStatusClass},
		{Name: "LA", Value: fmt.Sprintf("%.2f", clientHosting.LoadAverage), StatusClass: clientHosting.LoadStatusClass},
		{Name: "Memory", Value: firstNonEmpty(clientHosting.RAMTotalLabel, "память не передана"), StatusClass: "hosting-metric-ok"},
		{Name: "Disk", Value: diskLabel, StatusClass: clientHosting.DiskStatusClass},
		{Name: "Uptime", Value: clientHosting.ServerUptimeLabel, StatusClass: clientHosting.ServerUptimeClass},
	}
}

func FastServerHostings(clientHostings []ClientHosting) []ClientHosting {
	hostings := make([]ClientHosting, 0, len(clientHostings))
	for _, clientHosting := range clientHostings {
		if clientHostingLooksPublic(clientHosting) {
			hostings = append(hostings, clientHosting)
		}
	}
	return hostings
}

func RealClientHostings(clientHostings []ClientHosting, domainMatches func(string, string) bool) []ClientHosting {
	realHostings := make([]ClientHosting, 0, len(clientHostings))
	for _, clientHosting := range clientHostings {
		if ClientHostingIsRealServer(clientHosting, domainMatches) {
			realHostings = append(realHostings, clientHosting)
		}
	}
	return realHostings
}

func ClientHostingIsRealServer(clientHosting ClientHosting, domainMatches func(string, string) bool) bool {
	if !clientHostingLooksPublic(clientHosting) {
		return false
	}
	if domainMatches == nil {
		return true
	}
	return domainMatches(normalizeDomainName(clientHosting.ServerDomain), strings.TrimSpace(clientHosting.ServerIP))
}

func ServerClientCount(servers []ServerView) int {
	clientEmails := make(map[string]struct{})
	for _, server := range servers {
		for _, client := range server.Clients {
			email := strings.ToLower(strings.TrimSpace(client.Email))
			if email != "" {
				clientEmails[email] = struct{}{}
			}
		}
	}
	return len(clientEmails)
}

func DemoPaymentProviders(paymentURL string) []PaymentProvider {
	return []PaymentProvider{{
		Provider:     "sitebrush_com",
		Enabled:      true,
		DisplayName:  "SiteBrush.com demo payments",
		PaymentURL:   strings.TrimSpace(paymentURL),
		Instructions: "Предустановленная демо-оплата через SiteBrush.com.",
	}}
}

func HostingSyncIsStale(lastSeenAt string, now time.Time) bool {
	lastSeenAt = strings.TrimSpace(lastSeenAt)
	if lastSeenAt == "" {
		return true
	}
	parsedTime, err := time.Parse(time.RFC3339, lastSeenAt)
	if err != nil {
		return true
	}
	return now.Sub(parsedTime) > 7*24*time.Hour
}

func HostingDisplayName(clientHosting ClientHosting) string {
	return firstNonEmpty(clientHosting.ServerDomain, clientHosting.InstallationID)
}

func HostingStatusLabel(clientHosting ClientHosting) string {
	if strings.TrimSpace(clientHosting.ServerIP) != "" {
		return "IP " + strings.TrimSpace(clientHosting.ServerIP)
	}
	return firstNonEmpty(clientHosting.ServerStatus, "локальная инсталляция")
}

func ClientRolesForEmails(clientHosting ClientHosting, emails map[string]struct{}) []ClientHostingRole {
	roles := make([]ClientHostingRole, 0, len(clientHosting.Roles))
	for _, role := range clientHosting.Roles {
		email := strings.ToLower(strings.TrimSpace(role.Email))
		if email == "" {
			continue
		}
		if _, found := emails[email]; found {
			roles = append(roles, role)
		}
	}
	return roles
}

func HostingBelongsToClientEmails(clientHosting ClientHosting, emails map[string]struct{}) bool {
	for _, email := range append(clientHosting.ClientEmails, clientHosting.OwnerEmail) {
		email = strings.ToLower(strings.TrimSpace(email))
		if email == "" {
			continue
		}
		if _, found := emails[email]; found {
			return true
		}
	}
	return false
}

func HostingHasSpecialStatus(clientHosting ClientHosting) bool {
	serverIP := strings.TrimSpace(clientHosting.ServerIP)
	if serverIP == "" {
		return true
	}
	parsedIP := net.ParseIP(serverIP)
	return parsedIP == nil || parsedIP.IsLoopback() || parsedIP.IsPrivate() || parsedIP.IsUnspecified()
}

func DomainIsLocalDevelopment(domain string, sourceIP string) bool {
	domain = normalizeDomainName(domain)
	if domain == "" || sourceIP == "" {
		return true
	}
	if domain == "localhost" || strings.HasSuffix(domain, ".localhost") {
		return true
	}
	if net.ParseIP(domain) != nil {
		return true
	}
	parsedIP := net.ParseIP(strings.TrimSpace(sourceIP))
	return parsedIP == nil || parsedIP.IsLoopback() || parsedIP.IsPrivate() || parsedIP.IsUnspecified()
}

func InstallationStatus(domain string, sourceIP string) string {
	domain = normalizeDomainName(domain)
	sourceIP = strings.TrimSpace(sourceIP)
	if sourceIP == "" {
		return "сервер без публичного IP"
	}
	if domain == "localhost" || strings.HasSuffix(domain, ".localhost") {
		return "локальная инсталляция · " + sourceIP
	}
	if net.ParseIP(domain) != nil {
		return "тестовый сервер · " + sourceIP
	}
	parsedIP := net.ParseIP(sourceIP)
	if parsedIP == nil {
		return "IP " + sourceIP
	}
	if parsedIP.IsLoopback() || parsedIP.IsPrivate() || parsedIP.IsUnspecified() {
		return "частный IP " + sourceIP
	}
	return "IP " + sourceIP
}

func KnownParentDomain(domain string, knownSiteDomains map[string]struct{}) string {
	parentDomain := parentDomain(domain)
	if parentDomain == "" {
		return ""
	}
	if _, found := knownSiteDomains[parentDomain]; found {
		return parentDomain
	}
	return ""
}

func PlanPaidStatus(plan Plan) string {
	price := strings.TrimSpace(plan.Price)
	if price == "" || price == "0" || price == "0.00" {
		return "free"
	}
	return "paid"
}

func ClientPlansFromPlans(plans []Plan) []ClientHostingPlan {
	clientPlans := make([]ClientHostingPlan, 0, len(plans))
	for _, plan := range plans {
		clientPlans = append(clientPlans, ClientHostingPlan{
			Name:          plan.Name,
			QuotaLabel:    plan.QuotaLabel,
			SiteLimit:     plan.SiteLimit,
			Price:         plan.Price,
			Currency:      plan.Currency,
			BillingPeriod: plan.BillingPeriod,
			PaidStatus:    PlanPaidStatus(plan),
			IsDefault:     plan.IsDefault,
		})
	}
	return clientPlans
}

func ServerInvoiceViews(invoices []Invoice, domains map[string]struct{}, emails map[string]struct{}) []ServerInvoiceView {
	views := make([]ServerInvoiceView, 0, len(invoices))
	for _, invoice := range invoices {
		domain := normalizeDomainName(invoice.Domain)
		email := strings.ToLower(strings.TrimSpace(invoice.CustomerEmail))
		_, domainFound := domains[domain]
		_, emailFound := emails[email]
		if len(domains) > 0 && !domainFound && (len(emails) == 0 || !emailFound) {
			continue
		}
		statusLabel := InvoiceStatusLabel(invoice.Status)
		views = append(views, ServerInvoiceView{
			Number:         invoice.Number,
			CustomerEmail:  invoice.CustomerEmail,
			Domain:         invoice.Domain,
			PlanName:       firstNonEmpty(invoice.PlanName, "обслуживание сайта"),
			AmountLabel:    strings.TrimSpace(invoice.Amount + " " + invoice.Currency),
			StatusLabel:    statusLabel,
			PaymentURL:     invoice.PaymentURL,
			PeriodLabel:    InvoicePeriodLabel(invoice),
			HistoryLabel:   InvoiceHistoryLabel(invoice),
			RecurringLabel: InvoiceRecurringLabel(invoice),
			CanPay:         invoice.PaymentURL != "" && strings.TrimSpace(invoice.Status) != "paid" && strings.TrimSpace(invoice.Status) != "cancelled",
		})
	}
	return views
}

func InvoiceStatusLabel(status string) string {
	switch strings.TrimSpace(status) {
	case "issued":
		return "ожидает оплаты"
	case "paid":
		return "оплачен"
	case "payment_error":
		return "ошибка оплаты"
	case "cancelled":
		return "отменён"
	default:
		return firstNonEmpty(status, "неизвестно")
	}
}

func InvoicePeriodLabel(invoice Invoice) string {
	if strings.TrimSpace(invoice.DueAt) != "" {
		return "до " + strings.TrimSpace(invoice.DueAt)
	}
	return "текущий период обслуживания"
}

func InvoiceHistoryLabel(invoice Invoice) string {
	parts := []string{"создан " + strings.TrimSpace(invoice.CreatedAt)}
	if strings.TrimSpace(invoice.PaidAt) != "" {
		parts = append(parts, "оплачен "+strings.TrimSpace(invoice.PaidAt))
	}
	if strings.TrimSpace(invoice.UpdatedAt) != "" && strings.TrimSpace(invoice.UpdatedAt) != strings.TrimSpace(invoice.CreatedAt) {
		parts = append(parts, "обновлён "+strings.TrimSpace(invoice.UpdatedAt))
	}
	return strings.Join(parts, " · ")
}

func InvoiceRecurringLabel(invoice Invoice) string {
	if !invoice.Recurring {
		return "разовый счёт"
	}
	period := strings.TrimSpace(invoice.RecurringPeriod)
	if period == "" {
		period = "monthly"
	}
	return "периодический · " + period
}

func InvoiceLabelForDomain(invoices []Invoice, domain string) string {
	domain = normalizeDomainName(domain)
	if domain == "" {
		return "счёт не нужен"
	}
	for _, invoice := range invoices {
		if normalizeDomainName(invoice.Domain) != domain {
			continue
		}
		return invoice.Number + " · " + InvoiceStatusLabel(invoice.Status)
	}
	return "можно выставить счёт"
}

func BillableSiteCount(sites []ServerSiteView) int {
	count := 0
	for _, site := range sites {
		if normalizeDomainName(site.Domain) != "" && site.BillingBillable {
			count++
		}
	}
	return count
}

func UnpaidInvoiceCount(invoices []ServerInvoiceView) int {
	count := 0
	for _, invoice := range invoices {
		switch invoice.StatusLabel {
		case "ожидает оплаты", "ошибка оплаты":
			count++
		}
	}
	return count
}

func InvoiceAction(billableCount int, unpaidInvoiceCount int) (string, string) {
	if billableCount == 0 {
		return "Не выставлять счёт", "btn-outline-secondary"
	}
	if unpaidInvoiceCount > 0 {
		return "Проверить счета", "btn-outline-primary"
	}
	return "Выставить счёт", "btn-success"
}

func overviewActions(view OverviewView) []OverviewAction {
	actions := make([]OverviewAction, 0, 5)
	if view.PendingRequests > 0 {
		actions = append(actions, OverviewAction{Title: "Новые заявки", Text: strconv.Itoa(view.PendingRequests) + " ждут решения", ButtonText: "Открыть сайты", Tab: "sites", Level: "warning"})
	}
	if view.UnpaidInvoices > 0 {
		actions = append(actions, OverviewAction{Title: "История счетов", Text: strconv.Itoa(view.UnpaidInvoices) + " записей ожидают оплаты или требуют проверки", ButtonText: "Открыть клиентов", Tab: "clients", Level: "warning"})
	}
	if view.OverLimitSites > 0 {
		actions = append(actions, OverviewAction{Title: "Место на диске", Text: strconv.Itoa(view.OverLimitSites) + " сайтов превысили лимит", ButtonText: "Открыть сайты", Tab: "sites", Level: "danger"})
	}
	if view.StaleServers > 0 {
		actions = append(actions, OverviewAction{Title: "Синхронизация", Text: strconv.Itoa(view.StaleServers) + " серверов давно не обновлялись", ButtonText: "Открыть серверы", Tab: "overview", Level: "warning"})
	}
	if view.MailErrors > 0 {
		actions = append(actions, OverviewAction{Title: "Отправка писем", Text: strconv.Itoa(view.MailErrors) + " ошибок в журнале", ButtonText: "Открыть клиентов", Tab: "clients", Level: "danger"})
	}
	if len(actions) == 0 {
		actions = append(actions, OverviewAction{Title: "Работа идёт штатно", Text: "Можно смотреть клиентов, сайты и помесячный расход диска.", ButtonText: "Открыть клиентов", Tab: "clients", Level: "ok"})
	}
	return actions
}

func applyServerInvoiceDefaults(server *ServerView) {
	if server == nil {
		return
	}
	server.DefaultInvoiceCurrency = "EUR"
	server.DefaultInvoiceAmount = "0.00"
	if len(server.Clients) > 0 {
		server.DefaultInvoiceClient = server.Clients[0].Email
	}
	for _, site := range server.Sites {
		if server.DefaultInvoiceDomain == "" {
			server.DefaultInvoiceDomain = site.Domain
			server.DefaultInvoicePlan = site.PlanName
		}
		if site.BillingBillable {
			server.DefaultInvoiceDomain = site.Domain
			server.DefaultInvoiceClient = firstNonEmpty(site.OwnerEmail, server.DefaultInvoiceClient)
			server.DefaultInvoicePlan = site.PlanName
			if site.BillingAmount != "" {
				server.DefaultInvoiceAmount = site.BillingAmount
				server.DefaultInvoiceCurrency = firstNonEmpty(site.BillingCurrency, server.DefaultInvoiceCurrency)
			}
			return
		}
	}
}

func serverClientViewsFromSites(sites []ServerSiteView) []ServerClientView {
	clientSites := make(map[string][]ServerSiteView)
	for _, site := range sites {
		email := strings.ToLower(strings.TrimSpace(site.OwnerEmail))
		if email == "" {
			email = "owner not set"
		}
		clientSites[email] = append(clientSites[email], site)
	}
	clients := make([]ServerClientView, 0, len(clientSites))
	for email, sites := range clientSites {
		sort.SliceStable(sites, func(left, right int) bool {
			return normalizeDomainName(sites[left].Domain) < normalizeDomainName(sites[right].Domain)
		})
		domains := make([]string, 0, len(sites))
		for _, site := range sites {
			domains = append(domains, site.Domain)
		}
		domains = normalizedDomains(domains)
		clients = append(clients, ServerClientView{
			Email:     email,
			SiteCount: len(domains),
			Domains:   strings.Join(domains, ", "),
			Sites:     sites,
		})
	}
	sort.Slice(clients, func(left, right int) bool {
		if clients[left].SiteCount != clients[right].SiteCount {
			return clients[left].SiteCount > clients[right].SiteCount
		}
		return clients[left].Email < clients[right].Email
	})
	return clients
}

func normalizedDomains(rawDomains []string) []string {
	seen := make(map[string]struct{}, len(rawDomains))
	domains := make([]string, 0, len(rawDomains))
	for _, rawDomain := range rawDomains {
		domain := normalizeDomainName(rawDomain)
		if domain == "" {
			continue
		}
		if _, found := seen[domain]; found {
			continue
		}
		seen[domain] = struct{}{}
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	return domains
}

func serverDomains(sites []ServerSiteView) map[string]struct{} {
	domains := make(map[string]struct{}, len(sites))
	for _, site := range sites {
		domain := normalizeDomainName(site.Domain)
		if domain != "" {
			domains[domain] = struct{}{}
		}
	}
	return domains
}

func serverClientEmails(clients []ServerClientView) map[string]struct{} {
	emails := make(map[string]struct{}, len(clients))
	for _, client := range clients {
		email := strings.ToLower(strings.TrimSpace(client.Email))
		if email != "" {
			emails[email] = struct{}{}
		}
	}
	return emails
}

func serverTotalUsedLabel(sites []ServerSiteView) string {
	var totalBytes int64
	for _, site := range sites {
		parsedBytes, ok := parseSizeLabel(site.UsedLabel)
		if ok {
			totalBytes += parsedBytes
		}
	}
	if totalBytes <= 0 {
		return ""
	}
	return FormatFileSize(totalBytes)
}

func parseSizeLabel(label string) (int64, bool) {
	fields := strings.Fields(strings.TrimSpace(label))
	if len(fields) == 0 {
		return 0, false
	}
	value, err := strconv.ParseFloat(strings.ReplaceAll(fields[0], ",", "."), 64)
	if err != nil {
		return 0, false
	}
	multiplier := float64(1)
	if len(fields) > 1 {
		switch strings.ToLower(fields[1]) {
		case "kb", "kib", "кб":
			multiplier = 1024
		case "mb", "mib", "мб":
			multiplier = 1024 * 1024
		case "gb", "gib", "гб":
			multiplier = 1024 * 1024 * 1024
		case "tb", "tib", "тб":
			multiplier = 1024 * 1024 * 1024 * 1024
		}
	}
	return int64(value * multiplier), true
}

func clientHostingLooksPublic(clientHosting ClientHosting) bool {
	serverDomain := normalizeDomainName(clientHosting.ServerDomain)
	serverIP := strings.TrimSpace(clientHosting.ServerIP)
	parsedIP := net.ParseIP(serverIP)
	if parsedIP == nil || parsedIP.IsLoopback() || parsedIP.IsPrivate() || parsedIP.IsUnspecified() {
		return false
	}
	if serverDomain == "" || serverDomain == "localhost" || strings.HasSuffix(serverDomain, ".localhost") || net.ParseIP(serverDomain) != nil {
		return false
	}
	return true
}

func parentDomain(domain string) string {
	domain = normalizeDomainName(domain)
	parts := strings.Split(domain, ".")
	if len(parts) < 3 {
		return ""
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

func FirstHostingSnapshotEmail(emails []string) string {
	for _, email := range emails {
		if strings.TrimSpace(email) != "" {
			return strings.ToLower(strings.TrimSpace(email))
		}
	}
	return ""
}

func SplitEmailList(rawEmails string) []string {
	fields := strings.FieldsFunc(rawEmails, func(separator rune) bool {
		return separator == ',' || separator == '\n' || separator == ';' || separator == ' '
	})
	emails := make([]string, 0, len(fields))
	for _, field := range fields {
		email := strings.ToLower(strings.TrimSpace(field))
		if email != "" {
			emails = append(emails, email)
		}
	}
	return emails
}

func firstHostingSnapshotEmail(emails []string) string {
	return FirstHostingSnapshotEmail(emails)
}

func splitEmailList(rawEmails string) []string {
	return SplitEmailList(rawEmails)
}

func normalizeDomainName(rawDomain string) string {
	domain := strings.ToLower(strings.Trim(strings.TrimSpace(rawDomain), "."))
	if domain == "" {
		return ""
	}
	return domain
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
