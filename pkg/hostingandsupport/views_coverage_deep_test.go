package hostingandsupport

import (
	"strings"
	"testing"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/expenses"
)

func TestHostingServerViewsMergeSortAndPreserveLocalSecurityState(t *testing.T) {
	local := ServerView{
		ID: "local",
		Name: "example.com",
		Subtitle: "local",
		Sites: []ServerSiteView{{
			Domain:"site.example.com", OwnerEmail:"owner@example.com",
			LimitLabel:"10 GB", QuotaInput:"10gb", CertificateValid:true,
			CertificateExpiresAt:"future", CertificateRemaining:"30d", CertificateCanRenew:true,
		}},
		SiteCount:1,
		Clients:[]ServerClientView{{Email:"owner@example.com"}},
		ClientCount:1,
		Invoices:[]ServerInvoiceView{{Number:"INV-L"}},
		InvoiceCount:1,
		BillableCount:1,
		UnpaidInvoiceCount:1,
		InvoiceActionLabel:"local action",
		InvoiceActionClass:"local class",
		TotalUsedLabel:"1 GB",
	}
	remoteDuplicate := ClientHosting{
		InstallationID:"remote-1",
		ServerDomain:"example.com",
		ServerIP:"8.8.8.8",
		ServerStatus:"online",
		LastSeenAt:time.Now().UTC().Format(time.RFC3339),
		SitebrushVersion:"v1",
		Sites:[]ClientHostingSite{{
			Domain:"site.example.com", OwnerEmail:"owner@example.com", UsedBytes:100,
		}},
		SiteCount:1,
	}
	remoteOther := ClientHosting{
		InstallationID:"remote-2", ServerDomain:"z.example.net", ServerIP:"1.1.1.1",
		LastSeenAt:time.Now().UTC().Format(time.RFC3339), SitebrushVersion:"v0",
		Sites:[]ClientHostingSite{
			{Domain:"a.example.net",OwnerEmail:"a@example.net"},
			{Domain:"b.example.net",OwnerEmail:"b@example.net"},
		},
		SiteCount:2,
	}
	servers := BuildServerViews(local, []ClientHosting{remoteOther, remoteDuplicate}, nil, "v1")
	if len(servers)!=2 {
		t.Fatalf("server count=%d want 2: %#v",len(servers),servers)
	}
	if !servers[0].Local || servers[0].ID!="remote-1" {
		t.Fatalf("duplicate local/remote server not merged: %#v",servers[0])
	}
	if !servers[0].Sites[0].CanEditQuota || servers[0].Sites[0].QuotaInput!="10gb" || !servers[0].Sites[0].CertificateValid {
		t.Fatalf("local quota/certificate state was not preserved: %#v",servers[0].Sites[0])
	}
	if servers[0].InvoiceCount != 1 || servers[0].Invoices[0].Number!="INV-L" {
		t.Fatalf("local invoices not preserved on merge: %#v",servers[0].Invoices)
	}
	if servers[1].Name!="z.example.net" {
		t.Fatalf("remote sorting/name unexpected: %#v",servers[1])
	}

	if !serverViewsLookDuplicate(
		ServerView{Sites:[]ServerSiteView{{Domain:"A.EXAMPLE"}}},
		ServerView{Sites:[]ServerSiteView{{Domain:"a.example"}}},
	) {
		t.Fatal("same site set did not identify duplicate server")
	}
	if serverViewsLookDuplicate(ServerView{},ServerView{Sites:[]ServerSiteView{{Domain:"a.example"}}}) {
		t.Fatal("server with no local domains matched remote server")
	}
	if serverViewsLookDuplicate(
		ServerView{Sites:[]ServerSiteView{{Domain:"a.example"}}},
		ServerView{Sites:[]ServerSiteView{{Domain:"a.example"},{Domain:"b.example"}}},
	) {
		t.Fatal("partial domain overlap incorrectly merged servers")
	}
}

func TestHostingMetricViewsSparklineDiskAndBounds(t *testing.T) {
	history:=[]ServerResourceCheck{
		{CPUUsagePercent:-10,LoadAverage:-1},
		{CPUUsagePercent:50,LoadAverage:1},
		{CPUUsagePercent:200,LoadAverage:8},
	}
	host:=ClientHosting{
		CPUUsagePercent:125, CPUStatusClass:"hosting-metric-danger",
		CPUCores:2, LoadAverage:5,
		TopCPUProcessName:"worker",TopCPUProcessPID:42,TopCPUProcessPercent:88,
		RAMTotalLabel:"8 GB",ServerUptimeLabel:"2 days",ServerUptimeClass:"ok",
		NetworkUptimeLabel:"99.9%",NetworkStatusClass:"ok",LastResponseMS:20,
		ResourceHistory:history,
	}
	metrics:=ServerSystemMetricViews(host)
	if len(metrics)!=5 || metrics[0].Percent!=100 || !metrics[0].HasProcessModal || len(metrics[0].Processes)!=1 {
		t.Fatalf("CPU metrics=%#v",metrics)
	}
	if metrics[1].Percent!=100 || metrics[1].StatusClass!="hosting-metric-danger" || !metrics[1].HasSparkline {
		t.Fatalf("queue metrics=%#v",metrics[1])
	}
	if points:=serverMetricSparklinePoints(history,func(check ServerResourceCheck)float64{return check.CPUUsagePercent},100); strings.Count(points," ")!=2 {
		t.Fatalf("sparkline=%q",points)
	}
	if serverMetricSparklinePoints(history[:1],func(ServerResourceCheck)float64{return 1},100)!="" ||
		serverMetricSparklinePoints(history,func(ServerResourceCheck)float64{return 1},0)!="" {
		t.Fatal("invalid sparkline inputs produced points")
	}
	if loadAveragePercent(-1,0)!=0 || maxCPUCoreCount(0)!=1 || maxCPUCoreCount(8)!=8 {
		t.Fatal("load/core bounds failed")
	}
	if clampPercent(-1)!=0 || clampPercent(101)!=100 || clampPercent(55)!=55 {
		t.Fatal("percent clamp failed")
	}

	server:=ServerView{
		DiskStatusClass:"old",
		SystemMetrics:[]ServerMetricView{
			{Name:"CPU",Value:"10%"},
			{Name:" Disk ",Value:"75%",Detail:"75 GB занято / 25 GB свободно / 100 GB всего",StatusClass:"warning",Percent:75},
		},
	}
	applyServerDiskFromMetrics(&server)
	if len(server.SystemMetrics)!=1 || server.DiskUsedPercent!=75 || server.DiskUsedLabel!="75 GB" || server.DiskFreeLabel!="25 GB" || server.DiskTotalLabel!="100 GB" {
		t.Fatalf("disk metric extraction=%#v",server)
	}
	applyServerDiskFromMetrics(nil)
}

func TestHostingCostViewCoversCapacityInvoicesAndExcludedSites(t *testing.T) {
	server:=ServerView{
		ID:"install-1",
		Sites:[]ServerSiteView{
			{Domain:"paid.example",UsedBytes:2*expenses.DecimalGigabyte,BillingBillable:true},
			{Domain:"excluded.example",UsedBytes:expenses.DecimalGigabyte,BillingBillable:true,BillingExcluded:true},
		},
	}
	policy:=expenses.ServerPolicy{
		Mode:expenses.ModeActual,
		ActualMonthlyExpenseMinor:12000,
		Currency:"USD",
		DiskTotalBytes:10*expenses.DecimalGigabyte,
		FreeSiteThresholdBytes:expenses.DecimalMegabyte,
	}
	month:=time.Now().UTC().Format("2006-01")+"-01"
	invoices:=[]Invoice{{
		InstallationID:"install-1",Status:"paid",PeriodStart:month,ReserveMinor:500,
		Lines:[]InvoiceLine{{CostShareMinor:4000}},
	}}
	ApplyServerCostView(&server,policy,invoices)
	if !server.CostConfigured || !server.DiskCapacityKnown || server.BillingCurrency!="USD" {
		t.Fatalf("cost configuration=%#v",server)
	}
	if server.CoveredMonthLabel=="" || server.UncoveredMonthLabel=="" || server.ReserveLabel=="" ||
		server.CapacityCostPerGBLabel=="" || server.SharedCostPerGBLabel=="" {
		t.Fatalf("cost labels incomplete: %#v",server)
	}
	ApplyServerCostView(nil,policy,nil)

	local:=ServerView{ID:"local",Local:true}
	ApplyServerCostView(&local,policy,[]Invoice{{
		InstallationID:"local:test",Status:"paid",PeriodStart:month,
		Lines:[]InvoiceLine{{CostShareMinor:999999}},
	}})
	if strings.Contains(local.UncoveredMonthLabel,"-") {
		t.Fatalf("covered amount produced negative uncovered cost: %q",local.UncoveredMonthLabel)
	}
}

func TestHostingClassificationCoversServerDesktopTemporaryAndArchived(t *testing.T) {
	now:=time.Now().UTC().Truncate(time.Second)
	checked:=now.Add(-time.Minute).Format(time.RFC3339)
	started:=now.Add(-8*24*time.Hour)
	expectedSlots:=int((8*24*time.Hour)/DesktopPresenceInterval)+1
	input:=[]ClientHosting{
		{
			InstallationID:"server-good",InstallationKind:InstallationKindServer,
			ServerDomain:"node.example.com",ServerIP:"8.8.8.8",LastSeenAt:checked,
		},
		{
			InstallationID:"server-private",InstallationKind:InstallationKindServer,
			ServerDomain:"localhost",ServerIP:"127.0.0.1",LastSeenAt:checked,
		},
		{
			InstallationID:"desktop-good",InstallationKind:InstallationKindDesktop,SnapshotVersion:2,
			ServerIP:"1.1.1.1",LastSeenAt:checked,ObservationStartedAt:started.Format(time.RFC3339),
			PresenceSlots:expectedSlots,OwnerEmailVerified:true,OwnerEmail:"owner@example.com",
			Sites:[]ClientHostingSite{{
				Domain:"site.example.com",UsedBytes:123,OwnerEmail:"owner@example.com",
				AdminEmails:[]string{"admin@example.com"},ReachabilityCheckedAt:checked,
				DNSMatchesServer:true,ReachableByServer:true,
			}},
		},
		{
			InstallationID:"desktop-temp",InstallationKind:InstallationKindDesktop,SnapshotVersion:1,
			ServerIP:"",LastSeenAt:checked,ObservationStartedAt:now.Add(-time.Hour).Format(time.RFC3339),
		},
		{InstallationID:"old",LastSeenAt:now.Add(-2*HostingArchiveAfter).Format(time.RFC3339)},
	}
	production,temporary,archived:=ClassifyClientHostings(input,now)
	if len(production)!=2 || len(temporary)!=1 || len(archived)!=1 {
		t.Fatalf("classification production=%#v temp=%#v archived=%#v",production,temporary,archived)
	}
	var desktop ClientHosting
	for _,h:=range production { if h.InstallationID=="desktop-good" {desktop=h} }
	if !desktop.Qualified || desktop.SiteCount!=1 || desktop.TotalUsedBytes!=123 || len(desktop.ClientEmails)!=2 {
		t.Fatalf("qualified desktop recalculation=%#v",desktop)
	}
	if len(temporary[0].Hostings)!=1 || len(temporary[0].Hostings[0].QualificationReasons)==0 {
		t.Fatalf("temporary desktop=%#v",temporary)
	}
	if got:=verifiedClientHostingSites([]ClientHostingSite{
		{ReachabilityCheckedAt:"bad",DNSMatchesServer:true,ReachableByServer:true},
		{ReachabilityCheckedAt:checked,DNSMatchesServer:false,ReachableByServer:true},
		{ReachabilityCheckedAt:checked,DNSMatchesServer:true,ReachableByServer:true},
	},now); len(got)!=1 {
		t.Fatalf("verified sites=%#v",got)
	}
}
