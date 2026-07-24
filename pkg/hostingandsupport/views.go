package hostingandsupport

import (
	"fmt"
	"math"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"sitebrush/pkg/expenses"
)

var nonPublicHostingIPPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("2001:db8::/32"),
}

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
	OSLabel                string
	CPULabel               string
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
	SitebrushVersionLabel  string
	SitebrushVersionClass  string
	DiskUsedLabel          string
	DiskUsedPercent        int
	DiskStatusClass        string
	InvoiceActionLabel     string
	InvoiceActionClass     string
	DefaultInvoiceClient   string
	DefaultInvoiceDomain   string
	DefaultInvoicePlan     string
	DefaultInvoiceAmount   string
	DefaultInvoiceCurrency string
	CostConfigured         bool
	MonthlyCostLabel       string
	MinimumPriceGBLabel    string
	CostPerGBLabel         string
	CoveredMonthLabel      string
	UncoveredMonthLabel    string
	ReserveLabel           string
	BillingCurrency        string
	MonthlyCostInput       string
	MinimumPriceGBInput    string
	ExpenseMode            string
	DiskRatePer100GBLabel  string
	DiskRatePer100GBInput  string
	ActualExpenseInput     string
	FreeThresholdInput     string
	CapacityCostPerGBLabel string
	SharedCostPerGBLabel   string
	BillingCapacityLabel   string
	BillingCostPerGBLabel  string
	BillableUsedLabel      string
	AllocatedMonthLabel    string
	CapacityUsedPercent    int
	CapacityExceeded       bool
	DiskCapacityKnown      bool
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
	UsedBytes         int64
	UsedLabel         string
	LimitLabel        string
	QuotaInput        string
	CanEditQuota      bool
	OverLimit         bool
	InvoiceLabel      string
	BillingPriceLabel string
	BillingStatusText string
	BillingAmount     string
	BillingCurrency   string
	BillingBillable   bool
	IsDemo            bool
	BillingExcluded   bool
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
	Name            string
	Value           string
	Detail          string
	StatusClass     string
	Percent         int
	HasPercent      bool
	SparklinePoints string
	HasSparkline    bool
	HasProcessModal bool
	Processes       []HostingSnapshotProcess
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
	Sites          []Site
	Invoices       []Invoice
	Plans          []Plan
	Assignments    map[string]ServiceAssignment
	SystemMetrics  []ServerMetricView
	CompileVersion string
	MainDomain     string
	CurrentHost    string
	SiteURL        func(string) string
	CostPolicy     expenses.ServerPolicy
	OwnerEmails    []string
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

func BuildServerViews(localServer ServerView, clientHostings []ClientHosting, invoices []Invoice, latestSitebrushVersion string) []ServerView {
	servers := make([]ServerView, 0, len(clientHostings)+1)
	servers = append(servers, localServer)
	for _, clientHosting := range clientHostings {
		remoteServer := BuildRemoteServerView(clientHosting, invoices, latestSitebrushVersion)
		if len(servers) > 0 && serverViewsLookDuplicate(servers[0], remoteServer) {
			servers[0] = mergeLocalAndRemoteServerViews(servers[0], remoteServer)
			continue
		}
		servers = append(servers, remoteServer)
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

func serverViewsLookDuplicate(localServer ServerView, remoteServer ServerView) bool {
	localName := normalizeDomainName(localServer.Name)
	remoteName := normalizeDomainName(remoteServer.Name)
	if localName != "" && remoteName != "" && localName == remoteName {
		return true
	}
	localDomains := serverDomains(localServer.Sites)
	if len(localDomains) == 0 {
		return false
	}
	overlap := 0
	for _, remoteSite := range remoteServer.Sites {
		if _, found := localDomains[normalizeDomainName(remoteSite.Domain)]; found {
			overlap++
		}
	}
	return overlap > 0 && overlap == len(remoteServer.Sites)
}

func mergeLocalAndRemoteServerViews(localServer ServerView, remoteServer ServerView) ServerView {
	merged := remoteServer
	merged.Local = true
	if merged.ID == "" {
		merged.ID = localServer.ID
	}
	merged.Subtitle = firstNonEmpty(remoteServer.Subtitle, localServer.Subtitle)
	if len(merged.Sites) == 0 {
		merged.Sites = localServer.Sites
		merged.Clients = localServer.Clients
		merged.SiteCount = localServer.SiteCount
		merged.ClientCount = localServer.ClientCount
		merged.TotalUsedLabel = localServer.TotalUsedLabel
	} else {
		localSitesByDomain := make(map[string]ServerSiteView, len(localServer.Sites))
		for _, localSite := range localServer.Sites {
			localSitesByDomain[normalizeDomainName(localSite.Domain)] = localSite
		}
		for siteIndex := range merged.Sites {
			localSite, found := localSitesByDomain[normalizeDomainName(merged.Sites[siteIndex].Domain)]
			if !found {
				continue
			}
			merged.Sites[siteIndex].LimitLabel = localSite.LimitLabel
			merged.Sites[siteIndex].QuotaInput = localSite.QuotaInput
			merged.Sites[siteIndex].CanEditQuota = true
		}
		merged.Clients = serverClientViewsFromSites(merged.Sites)
	}
	if len(merged.Invoices) == 0 {
		merged.Invoices = localServer.Invoices
		merged.InvoiceCount = localServer.InvoiceCount
		merged.BillableCount = localServer.BillableCount
		merged.UnpaidInvoiceCount = localServer.UnpaidInvoiceCount
		merged.InvoiceActionLabel = localServer.InvoiceActionLabel
		merged.InvoiceActionClass = localServer.InvoiceActionClass
	}
	return merged
}

func BuildLocalServerView(input LocalServerViewInput) ServerView {
	serverName := firstNonEmpty(normalizeDomainName(input.MainDomain), normalizeDomainName(input.CurrentHost), "SiteBrush.com")
	compileVersion := firstNonEmpty(strings.TrimSpace(input.CompileVersion), "unknown")
	server := ServerView{
		ID:                    "local",
		Name:                  serverName,
		Subtitle:              "локальный сервер SiteBrush",
		Local:                 true,
		SyncStatusLabel:       "локальные данные",
		SyncStatusClass:       "billing-sync-ok",
		NetworkStatusLabel:    "проверяется фоновым мониторингом",
		NetworkStatusClass:    "hosting-metric-ok",
		SitebrushVersionLabel: sitebrushVersionLabel(compileVersion, compileVersion),
		SitebrushVersionClass: "hosting-metric-ok",
		SystemMetrics:         input.SystemMetrics,
	}
	if len(server.SystemMetrics) == 0 {
		server.SystemMetrics = []ServerMetricView{
			{Name: "Статус", Value: "локальные данные", StatusClass: "hosting-metric-ok"},
		}
	}
	server.OSLabel, server.CPULabel = serverIdentityLabelsFromMetrics(server.SystemMetrics)
	ownerEmails := make(map[string]struct{}, len(input.OwnerEmails))
	for _, email := range input.OwnerEmails {
		ownerEmails[strings.ToLower(strings.TrimSpace(email))] = struct{}{}
	}
	for _, siteRow := range input.Sites {
		if siteRow.IsDemo {
			continue
		}
		ownerEmail := firstHostingSnapshotEmail(splitEmailList(siteRow.AdminEmails))
		if ownerEmail == "" {
			ownerEmail = "owner not set"
		}
		siteURL := siteRow.URL
		if input.SiteURL != nil {
			siteURL = input.SiteURL(siteRow.Domain)
		}
		billingExcluded := siteRow.IsDemo
		for _, email := range splitEmailList(siteRow.AdminEmails) {
			if _, isOwner := ownerEmails[strings.ToLower(strings.TrimSpace(email))]; isOwner {
				billingExcluded = true
			}
		}
		server.Sites = append(server.Sites, ServerSiteView{
			Domain:            siteRow.Domain,
			URL:               siteURL,
			OwnerEmail:        ownerEmail,
			AdminEmails:       siteRow.AdminEmails,
			PlanName:          "Дисковое пространство",
			PaidStatus:        siteRow.BillingStatusText,
			UsedBytes:         siteRow.UsedBytes,
			UsedLabel:         siteRow.UsedLabel,
			LimitLabel:        siteRow.LimitLabel,
			QuotaInput:        siteRow.QuotaInput,
			CanEditQuota:      true,
			OverLimit:         siteRow.UsedPercent >= 100,
			InvoiceLabel:      InvoiceLabelForDomain(input.Invoices, siteRow.Domain),
			BillingPriceLabel: siteRow.BillingPriceLabel,
			BillingStatusText: siteRow.BillingStatusText,
			BillingAmount:     siteRow.BillingAmount,
			BillingCurrency:   siteRow.BillingCurrency,
			BillingBillable:   siteRow.BillingBillable && !billingExcluded,
			IsDemo:            siteRow.IsDemo,
			BillingExcluded:   billingExcluded,
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
	applyServerDiskFromMetrics(&server)
	applyServerInvoiceDefaults(&server)
	ApplyServerCostView(&server, input.CostPolicy, input.Invoices)
	return server
}

func BuildRemoteServerView(clientHosting ClientHosting, invoices []Invoice, latestSitebrushVersion string) ServerView {
	sitebrushVersionLabel, sitebrushVersionClass := remoteSitebrushVersionDisplay(clientHosting.SitebrushVersion, latestSitebrushVersion)
	server := ServerView{
		ID:                    strings.TrimSpace(clientHosting.InstallationID),
		Name:                  firstNonEmpty(clientHosting.ServerDomain, clientHosting.InstallationID),
		Subtitle:              firstNonEmpty(clientHosting.ServerStatus, clientHosting.ServerIP, "удалённый сервер SiteBrush"),
		OwnerEmail:            clientHosting.OwnerEmail,
		OSLabel:               firstNonEmpty(strings.TrimSpace(clientHosting.OSName+" "+clientHosting.OSVersion), "ОС не передана"),
		CPULabel:              fmt.Sprintf("%s · %d ядер", firstNonEmpty(clientHosting.CPUModel, "CPU не передан"), clientHosting.CPUCores),
		SiteCount:             clientHosting.SiteCount,
		TotalUsedLabel:        clientHosting.TotalUsedLabel,
		DiskUsedLabel:         clientHosting.DiskUsedLabel,
		DiskFreeLabel:         clientHosting.DiskFreeLabel,
		DiskTotalLabel:        clientHosting.DiskTotalLabel,
		DiskUsedPercent:       clientHosting.DiskUsedPercent,
		DiskStatusClass:       clientHosting.DiskStatusClass,
		NetworkStatusLabel:    clientHosting.NetworkUptimeLabel,
		NetworkStatusClass:    clientHosting.NetworkStatusClass,
		SitebrushVersionLabel: sitebrushVersionLabel,
		SitebrushVersionClass: sitebrushVersionClass,
		SystemMetrics:         ServerSystemMetricViews(clientHosting),
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
	ownerEmails := make(map[string]struct{})
	for _, role := range clientHosting.Roles {
		if role.Role == "superadmin" {
			ownerEmails[strings.ToLower(strings.TrimSpace(role.Email))] = struct{}{}
		}
	}
	expenseSites := make([]expenses.SiteUsage, 0, len(clientHosting.Sites))
	for siteIndex, site := range clientHosting.Sites {
		billingExcluded := site.IsDemo
		for _, email := range append(append([]string(nil), site.AdminEmails...), site.OwnerEmail) {
			if _, isOwner := ownerEmails[strings.ToLower(strings.TrimSpace(email))]; isOwner {
				billingExcluded = true
			}
		}
		expenseSites = append(expenseSites, expenses.SiteUsage{
			Key: strconv.Itoa(siteIndex), UsedBytes: site.UsedBytes, Excluded: billingExcluded,
		})
	}
	expensePolicy := clientHosting.ExpensePolicy
	expensePolicy.DiskTotalBytes = clientHosting.DiskTotalBytes
	expenseAllocation := expenses.AllocateMonthlyExpense(expensePolicy, expenseSites)
	for siteIndex, site := range clientHosting.Sites {
		if site.IsDemo {
			continue
		}
		ownerEmail := firstNonEmpty(site.OwnerEmail, firstHostingSnapshotEmail(site.AdminEmails), clientHosting.OwnerEmail, "owner not set")
		allocation := expenseAllocation.Sites[strconv.Itoa(siteIndex)]
		billingPrice := BillingPrice{
			UsedMegabytes:     bytesToRoundedMegabytes(site.UsedBytes),
			BillableMegabytes: bytesToRoundedMegabytes(site.UsedBytes),
			Amount:            formatMoneyMinor(allocation.ExpenseShareMinor),
			Currency:          expensePolicy.Currency,
			PriceLabel:        MoneyLabel(allocation.ExpenseShareMinor, expensePolicy.Currency) + "/мес",
			StatusText:        "доля расходов",
			Billable:          allocation.ExpenseShareMinor > 0,
		}
		if allocation.Free {
			billingPrice.PriceLabel = MoneyLabel(0, expensePolicy.Currency)
			billingPrice.StatusText = "бесплатно до " + FormatFileSize(expensePolicy.FreeSiteThresholdBytes)
		}
		billingExcluded := site.IsDemo
		for _, email := range append(append([]string(nil), site.AdminEmails...), site.OwnerEmail) {
			if _, isOwner := ownerEmails[strings.ToLower(strings.TrimSpace(email))]; isOwner {
				billingExcluded = true
			}
		}
		server.Sites = append(server.Sites, ServerSiteView{
			Domain:            site.Domain,
			URL:               "http://" + site.Domain + "/",
			OwnerEmail:        ownerEmail,
			AdminEmails:       strings.Join(site.AdminEmails, ", "),
			PlanName:          "Дисковое пространство",
			PaidStatus:        billingPrice.StatusText,
			UsedBytes:         site.UsedBytes,
			UsedLabel:         site.UsedLabel,
			LimitLabel:        site.LimitLabel,
			OverLimit:         site.OverLimit,
			InvoiceLabel:      InvoiceLabelForDomain(invoices, site.Domain),
			BillingPriceLabel: billingPrice.PriceLabel,
			BillingStatusText: billingPrice.StatusText,
			BillingAmount:     billingPrice.Amount,
			BillingCurrency:   billingPrice.Currency,
			BillingBillable:   billingPrice.Billable && !billingExcluded,
			IsDemo:            site.IsDemo,
			BillingExcluded:   billingExcluded,
		})
	}
	server.SiteCount = len(server.Sites)
	server.TotalUsedLabel = serverTotalUsedLabel(server.Sites)
	server.Clients = serverClientViewsFromSites(server.Sites)
	server.ClientCount = len(server.Clients)
	server.Invoices = ServerInvoiceViews(invoices, serverDomains(server.Sites), serverClientEmails(server.Clients))
	server.InvoiceCount = len(server.Invoices)
	server.BillableCount = BillableSiteCount(server.Sites)
	server.UnpaidInvoiceCount = UnpaidInvoiceCount(server.Invoices)
	server.InvoiceActionLabel, server.InvoiceActionClass = InvoiceAction(server.BillableCount, server.UnpaidInvoiceCount)
	applyServerInvoiceDefaults(&server)
	ApplyServerCostView(&server, expensePolicy, invoices)
	return server
}

func ClientHostingsWithDemoDomain(clientHostings []ClientHosting, demoDomain string) []ClientHosting {
	normalizedDemoDomain := normalizeDomainName(demoDomain)
	if normalizedDemoDomain == "" {
		return clientHostings
	}
	classifiedHostings := append([]ClientHosting(nil), clientHostings...)
	for hostingIndex := range classifiedHostings {
		classifiedHostings[hostingIndex].Sites = append([]ClientHostingSite(nil), classifiedHostings[hostingIndex].Sites...)
		for siteIndex := range classifiedHostings[hostingIndex].Sites {
			site := &classifiedHostings[hostingIndex].Sites[siteIndex]
			if normalizeDomainName(site.Domain) == normalizedDemoDomain {
				site.IsDemo = true
			}
		}
	}
	return classifiedHostings
}

func ApplyServerCostView(server *ServerView, policy expenses.ServerPolicy, invoices []Invoice) {
	if server == nil {
		return
	}
	policy = expenses.NormalizePolicy(policy)
	monthlyExpenseMinor := expenses.CalculateMonthlyExpense(policy)
	server.BillingCurrency = firstNonEmpty(normalizeBillingCurrency(policy.Currency), "EUR")
	server.ExpenseMode = string(policy.Mode)
	server.MonthlyCostInput = formatMoneyMinor(monthlyExpenseMinor)
	server.ActualExpenseInput = formatMoneyMinor(policy.ActualMonthlyExpenseMinor)
	server.DiskRatePer100GBInput = formatMoneyMinor(policy.DiskRatePer100GBMinor)
	server.DiskRatePer100GBLabel = MoneyLabel(policy.DiskRatePer100GBMinor, server.BillingCurrency)
	server.FreeThresholdInput = strconv.FormatInt(policy.FreeSiteThresholdBytes/expenses.DecimalMegabyte, 10)
	server.MonthlyCostLabel = MoneyLabel(monthlyExpenseMinor, server.BillingCurrency)
	server.CostConfigured = monthlyExpenseMinor > 0 && normalizeBillingCurrency(policy.Currency) != ""
	expenseSites := make([]expenses.SiteUsage, 0, len(server.Sites))
	for siteIndex, site := range server.Sites {
		expenseSites = append(expenseSites, expenses.SiteUsage{
			Key: strconv.Itoa(siteIndex), UsedBytes: site.UsedBytes, Excluded: site.BillingExcluded,
		})
	}
	allocation := expenses.AllocateMonthlyExpense(policy, expenseSites)
	server.BillingCapacityLabel = FormatFileSize(allocation.BillingCapacityBytes)
	server.BillableUsedLabel = FormatFileSize(allocation.BillableUsedBytes)
	server.AllocatedMonthLabel = MoneyLabel(allocation.AllocatedMinor, server.BillingCurrency)
	server.CapacityExceeded = allocation.CapacityExceeded
	server.DiskCapacityKnown = policy.DiskTotalBytes > 0
	if allocation.BillingCapacityBytes > 0 {
		server.BillingCostPerGBLabel = MoneyLabel(expenses.BillingCostPerGBMinor(policy), server.BillingCurrency)
		server.CapacityUsedPercent = int(math.Round(float64(allocation.BillableUsedBytes) / float64(allocation.BillingCapacityBytes) * 100))
		if server.CapacityUsedPercent > 100 {
			server.CapacityUsedPercent = 100
		}
	}
	if policy.DiskTotalBytes > 0 {
		capacityCostPerGBMinor := (monthlyExpenseMinor*expenses.DecimalGigabyte + policy.DiskTotalBytes/2) / policy.DiskTotalBytes
		server.CapacityCostPerGBLabel = MoneyLabel(capacityCostPerGBMinor, server.BillingCurrency)
	}
	totalBillableBytes := int64(0)
	for _, site := range server.Sites {
		if site.BillingExcluded || !site.BillingBillable {
			continue
		}
		totalBillableBytes += site.UsedBytes
	}
	if totalBillableBytes > 0 {
		sharedCostPerGBMinor := (monthlyExpenseMinor*expenses.DecimalGigabyte + totalBillableBytes/2) / totalBillableBytes
		server.SharedCostPerGBLabel = MoneyLabel(sharedCostPerGBMinor, server.BillingCurrency)
		server.CostPerGBLabel = server.SharedCostPerGBLabel
	}
	monthPrefix := time.Now().UTC().Format("2006-01")
	coveredMinor := int64(0)
	reserveMinor := int64(0)
	for _, invoice := range invoices {
		installationMatches := invoice.InstallationID == server.ID || (server.Local && strings.HasPrefix(invoice.InstallationID, "local:"))
		if !installationMatches || invoice.Status != "paid" {
			continue
		}
		reserveMinor += invoice.ReserveMinor
		if strings.HasPrefix(invoice.PeriodStart, monthPrefix) {
			for _, line := range invoice.Lines {
				coveredMinor += line.CostShareMinor
			}
		}
	}
	uncoveredMinor := monthlyExpenseMinor - coveredMinor
	if uncoveredMinor < 0 {
		uncoveredMinor = 0
	}
	server.CoveredMonthLabel = MoneyLabel(coveredMinor, server.BillingCurrency)
	server.UncoveredMonthLabel = MoneyLabel(uncoveredMinor, server.BillingCurrency)
	server.ReserveLabel = MoneyLabel(reserveMinor, server.BillingCurrency)
}

func ServerSystemMetricViews(clientHosting ClientHosting) []ServerMetricView {
	topCPUProcesses := clientHosting.TopCPUProcesses
	if len(topCPUProcesses) == 0 && strings.TrimSpace(clientHosting.TopCPUProcessName) != "" {
		topCPUProcesses = []HostingSnapshotProcess{{Name: strings.TrimSpace(clientHosting.TopCPUProcessName), PID: clientHosting.TopCPUProcessPID, CPUPercent: clientHosting.TopCPUProcessPercent}}
	}
	queuePercent := loadAveragePercent(clientHosting.LoadAverage, clientHosting.CPUCores)
	return []ServerMetricView{
		{Name: "CPU", Value: fmt.Sprintf("%.1f%%", clientHosting.CPUUsagePercent), Detail: "загрузка сейчас", StatusClass: clientHosting.CPUStatusClass, Percent: clampPercent(int(math.Round(clientHosting.CPUUsagePercent))), HasPercent: true, SparklinePoints: serverMetricSparklinePoints(clientHosting.ResourceHistory, func(check ServerResourceCheck) float64 { return check.CPUUsagePercent }, 100), HasSparkline: len(clientHosting.ResourceHistory) > 1, HasProcessModal: clientHosting.CPUStatusClass == "hosting-metric-danger" && len(topCPUProcesses) > 0, Processes: topCPUProcesses},
		{Name: "Очередь", Value: strconv.Itoa(queuePercent) + "%", Detail: fmt.Sprintf("LA %.2f / %d ядер", clientHosting.LoadAverage, maxCPUCoreCount(clientHosting.CPUCores)), StatusClass: QueueStatusClass(queuePercent), Percent: clampPercent(queuePercent), HasPercent: true, SparklinePoints: serverMetricSparklinePoints(clientHosting.ResourceHistory, func(check ServerResourceCheck) float64 { return check.LoadAverage }, math.Max(1, float64(clientHosting.CPUCores))), HasSparkline: len(clientHosting.ResourceHistory) > 1},
		{Name: "RAM", Value: firstNonEmpty(clientHosting.RAMTotalLabel, "память не передана"), Detail: "всего на сервере", StatusClass: "hosting-metric-ok"},
		{Name: "Uptime", Value: clientHosting.ServerUptimeLabel, Detail: "без перезапуска", StatusClass: clientHosting.ServerUptimeClass},
		{Name: "Доступность", Value: clientHosting.NetworkUptimeLabel, Detail: fmt.Sprintf("последний ответ %d ms", clientHosting.LastResponseMS), StatusClass: clientHosting.NetworkStatusClass},
	}
}

func serverIdentityLabelsFromMetrics(metrics []ServerMetricView) (string, string) {
	osLabel := "ОС не передана"
	cpuLabel := "CPU не передан"
	for _, metric := range metrics {
		switch strings.TrimSpace(metric.Name) {
		case "OS":
			osLabel = firstNonEmpty(metric.Value, osLabel)
		case "CPU":
			if !strings.Contains(metric.Value, "%") {
				cpuLabel = firstNonEmpty(metric.Value, cpuLabel)
			}
		}
	}
	return osLabel, cpuLabel
}

func serverMetricSparklinePoints(history []ServerResourceCheck, value func(ServerResourceCheck) float64, maxValue float64) string {
	if len(history) < 2 || maxValue <= 0 {
		return ""
	}
	points := make([]string, 0, len(history))
	lastIndex := len(history) - 1
	for index, check := range history {
		x := 0.0
		if lastIndex > 0 {
			x = float64(index) * 100 / float64(lastIndex)
		}
		yValue := value(check)
		if yValue < 0 {
			yValue = 0
		}
		if yValue > maxValue {
			yValue = maxValue
		}
		y := 28 - (yValue / maxValue * 24) - 2
		points = append(points, fmt.Sprintf("%.1f,%.1f", x, y))
	}
	return strings.Join(points, " ")
}

func loadAveragePercent(loadAverage float64, cpuCores int) int {
	maxValue := float64(maxCPUCoreCount(cpuCores))
	if loadAverage < 0 {
		loadAverage = 0
	}
	return int(math.Round(loadAverage / maxValue * 100))
}

func maxCPUCoreCount(cpuCores int) int {
	if cpuCores <= 0 {
		return 1
	}
	return cpuCores
}

func QueueStatusClass(queuePercent int) string {
	if queuePercent > 200 {
		return "hosting-metric-danger"
	}
	if queuePercent > 100 {
		return "hosting-metric-warning"
	}
	return "hosting-metric-ok"
}

func sitebrushVersionLabel(serverVersion string, latestVersion string) string {
	serverVersion = firstNonEmpty(strings.TrimSpace(serverVersion), "unknown")
	latestVersion = firstNonEmpty(strings.TrimSpace(latestVersion), "unknown")
	label := serverVersion + " (актуальная " + latestVersion
	if serverVersion != latestVersion {
		label += ", устарело"
	}
	return label + ")"
}

func remoteSitebrushVersionDisplay(serverVersion string, latestVersion string) (string, string) {
	serverVersion = firstNonEmpty(strings.TrimSpace(serverVersion), "unknown")
	latestVersion = firstNonEmpty(strings.TrimSpace(latestVersion), "unknown")
	statusClass := "hosting-metric-ok"
	if serverVersion != latestVersion {
		statusClass = "hosting-metric-warning"
	}
	return sitebrushVersionLabel(serverVersion, latestVersion), statusClass
}

func applyServerDiskFromMetrics(server *ServerView) {
	if server == nil {
		return
	}
	metrics := make([]ServerMetricView, 0, len(server.SystemMetrics))
	for _, metric := range server.SystemMetrics {
		if strings.TrimSpace(metric.Name) == "Disk" {
			server.DiskStatusClass = firstNonEmpty(metric.StatusClass, server.DiskStatusClass)
			server.DiskUsedPercent = metric.Percent
			if strings.HasSuffix(strings.TrimSpace(metric.Value), "%") {
				server.DiskUsedLabel = strings.TrimSpace(metric.Value)
			}
			diskLabels := strings.Split(metric.Detail, " / ")
			if len(diskLabels) == 3 {
				server.DiskUsedLabel = strings.TrimSuffix(strings.TrimSpace(diskLabels[0]), " занято")
				server.DiskFreeLabel = strings.TrimSuffix(strings.TrimSpace(diskLabels[1]), " свободно")
				server.DiskTotalLabel = strings.TrimSuffix(strings.TrimSpace(diskLabels[2]), " всего")
			}
			continue
		}
		metrics = append(metrics, metric)
	}
	server.SystemMetrics = metrics
}

func clampPercent(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
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

const DesktopQualificationMinimumAge = 7 * 24 * time.Hour
const DesktopQualificationMinimumAvailability = 90.0
const HostingSnapshotFreshness = 15 * time.Minute
const HostingArchiveAfter = 24 * time.Hour

const QualificationReasonUpdateRequired = "update_required"
const QualificationReasonObservation = "observation_period"
const QualificationReasonAvailability = "availability"
const QualificationReasonSnapshotStale = "snapshot_stale"
const QualificationReasonEmail = "email_unverified"
const QualificationReasonPublicIP = "public_ip"
const QualificationReasonDomain = "domain_unverified"

type DesktopHostingGroup struct {
	ServerIP string
	Hostings []ClientHosting
}

func ClassifyClientHostings(clientHostings []ClientHosting, now time.Time) ([]ClientHosting, []DesktopHostingGroup, []ClientHosting) {
	production := make([]ClientHosting, 0, len(clientHostings))
	temporary := make([]ClientHosting, 0, len(clientHostings))
	archived := make([]ClientHosting, 0, len(clientHostings))
	for _, clientHosting := range clientHostings {
		clientHosting = classifyClientHosting(clientHosting, now)
		if clientHosting.Archived {
			archived = append(archived, clientHosting)
			continue
		}
		if clientHosting.Qualified {
			production = append(production, clientHosting)
			continue
		}
		if clientHosting.InstallationKind == InstallationKindServer {
			continue
		}
		temporary = append(temporary, clientHosting)
	}
	return production, groupDesktopHostings(temporary), archived
}

func classifyClientHosting(clientHosting ClientHosting, now time.Time) ClientHosting {
	lastSeenAt, lastSeenValid := parseHostingTime(clientHosting.LastSeenAt)
	clientHosting.Archived = !lastSeenValid || now.Sub(lastSeenAt) > HostingArchiveAfter
	if clientHosting.Archived {
		return clientHosting
	}
	if clientHosting.InstallationKind == InstallationKindServer {
		clientHosting.Qualified = clientHostingLooksPublic(clientHosting)
		if !clientHosting.Qualified {
			clientHosting.QualificationReasons = []string{QualificationReasonPublicIP}
		}
		return clientHosting
	}
	if clientHosting.SnapshotVersion < 2 || clientHosting.InstallationKind != InstallationKindDesktop {
		clientHosting.QualificationReasons = append(clientHosting.QualificationReasons, QualificationReasonUpdateRequired)
	}
	observationStartedAt, observationValid := parseHostingTime(clientHosting.ObservationStartedAt)
	observationAge := time.Duration(0)
	if observationValid {
		observationAge = now.Sub(observationStartedAt)
		expectedSlots := int(observationAge/DesktopPresenceInterval) + 1
		if expectedSlots > 0 {
			observedSlots := clientHosting.PresenceSlots
			if observedSlots > expectedSlots {
				observedSlots = expectedSlots
			}
			clientHosting.AvailabilityPercent = math.Round(float64(observedSlots)/float64(expectedSlots)*10000) / 100
		}
	}
	clientHosting.AvailabilityLabel = fmt.Sprintf("%.2f%%", clientHosting.AvailabilityPercent)
	if !observationValid || observationAge < DesktopQualificationMinimumAge {
		clientHosting.QualificationReasons = append(clientHosting.QualificationReasons, QualificationReasonObservation)
	}
	if clientHosting.AvailabilityPercent < DesktopQualificationMinimumAvailability {
		clientHosting.QualificationReasons = append(clientHosting.QualificationReasons, QualificationReasonAvailability)
	}
	if now.Sub(lastSeenAt) > HostingSnapshotFreshness {
		clientHosting.QualificationReasons = append(clientHosting.QualificationReasons, QualificationReasonSnapshotStale)
	}
	if !clientHosting.OwnerEmailVerified {
		clientHosting.QualificationReasons = append(clientHosting.QualificationReasons, QualificationReasonEmail)
	}
	if !clientHostingHasPublicIP(clientHosting) {
		clientHosting.QualificationReasons = append(clientHosting.QualificationReasons, QualificationReasonPublicIP)
	}
	verifiedSites := verifiedClientHostingSites(clientHosting.Sites, now)
	if len(verifiedSites) == 0 {
		clientHosting.QualificationReasons = append(clientHosting.QualificationReasons, QualificationReasonDomain)
	}
	clientHosting.Qualified = len(clientHosting.QualificationReasons) == 0
	if clientHosting.Qualified {
		clientHosting.Sites = verifiedSites
		recalculateClientHostingSites(&clientHosting)
	}
	return clientHosting
}

func verifiedClientHostingSites(sites []ClientHostingSite, now time.Time) []ClientHostingSite {
	verifiedSites := make([]ClientHostingSite, 0, len(sites))
	for _, site := range sites {
		checkedAt, valid := parseHostingTime(site.ReachabilityCheckedAt)
		if valid && now.Sub(checkedAt) <= HostingSnapshotFreshness && site.DNSMatchesServer && site.ReachableByServer {
			verifiedSites = append(verifiedSites, site)
		}
	}
	return verifiedSites
}

func recalculateClientHostingSites(clientHosting *ClientHosting) {
	clientHosting.SiteCount = len(clientHosting.Sites)
	clientHosting.TotalUsedBytes = 0
	emails := make(map[string]struct{})
	if email := strings.TrimSpace(clientHosting.OwnerEmail); email != "" {
		emails[email] = struct{}{}
	}
	for _, site := range clientHosting.Sites {
		clientHosting.TotalUsedBytes += site.UsedBytes
		if email := strings.TrimSpace(site.OwnerEmail); email != "" {
			emails[email] = struct{}{}
		}
		for _, email := range site.AdminEmails {
			emails[email] = struct{}{}
		}
	}
	clientHosting.TotalUsedLabel = FormatFileSize(clientHosting.TotalUsedBytes)
	clientHosting.ClientEmails = sortedStringsFromMap(emails)
}

func groupDesktopHostings(hostings []ClientHosting) []DesktopHostingGroup {
	groupIndexByIP := make(map[string]int)
	groups := make([]DesktopHostingGroup, 0, len(hostings))
	for _, hosting := range hostings {
		serverIP := strings.TrimSpace(hosting.ServerIP)
		if serverIP == "" {
			serverIP = "unknown"
		}
		groupIndex, found := groupIndexByIP[serverIP]
		if !found {
			groupIndex = len(groups)
			groupIndexByIP[serverIP] = groupIndex
			groups = append(groups, DesktopHostingGroup{ServerIP: serverIP})
		}
		groups[groupIndex].Hostings = append(groups[groupIndex].Hostings, hosting)
	}
	sort.Slice(groups, func(leftIndex, rightIndex int) bool {
		return groups[leftIndex].ServerIP < groups[rightIndex].ServerIP
	})
	return groups
}

func parseHostingTime(rawTime string) (time.Time, bool) {
	parsedTime, err := time.Parse(time.RFC3339, strings.TrimSpace(rawTime))
	return parsedTime, err == nil
}

func ClientHostingLooksPublic(clientHosting ClientHosting) bool {
	return clientHostingLooksPublic(clientHosting)
}

func ClientHostingHasPublicIP(clientHosting ClientHosting) bool {
	return clientHostingHasPublicIP(clientHosting)
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
		if site.IsDemo {
			continue
		}
		email := strings.ToLower(strings.TrimSpace(site.OwnerEmail))
		if email == "" {
			email = "owner not set"
		}
		clientSites[email] = append(clientSites[email], site)
	}
	clients := make([]ServerClientView, 0, len(clientSites))
	for email, sites := range clientSites {
		sort.SliceStable(sites, func(left, right int) bool {
			if sites[left].UsedBytes != sites[right].UsedBytes {
				return sites[left].UsedBytes > sites[right].UsedBytes
			}
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
		if site.UsedBytes > 0 {
			totalBytes += site.UsedBytes
			continue
		}
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
	parsedIP := net.ParseIP(strings.TrimSpace(clientHosting.ServerIP))
	if parsedIP == nil || parsedIP.IsLoopback() || parsedIP.IsPrivate() || parsedIP.IsUnspecified() {
		return false
	}
	if serverDomain == "" || serverDomain == "localhost" || strings.HasSuffix(serverDomain, ".localhost") || net.ParseIP(serverDomain) != nil {
		return false
	}
	return true
}

func clientHostingHasPublicIP(clientHosting ClientHosting) bool {
	parsedIP, err := netip.ParseAddr(strings.TrimSpace(clientHosting.ServerIP))
	if err != nil {
		return false
	}
	parsedIP = parsedIP.Unmap()
	if !parsedIP.IsGlobalUnicast() || parsedIP.IsPrivate() || parsedIP.IsLoopback() || parsedIP.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range nonPublicHostingIPPrefixes {
		if prefix.Contains(parsedIP) {
			return false
		}
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
