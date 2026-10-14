(function conversionDashboard() {
    'use strict';
    const dashboard = document.getElementById('conversion-dashboard');
    if (!dashboard) return;
    const translations = new Map();
    for (const labelElement of document.querySelectorAll('#conversion-labels [data-translation]')) translations.set(labelElement.dataset.translation,labelElement.textContent);
    function label(labelKey) { return translations.get(labelKey) || labelKey; }
    const model = {
        report: JSON.parse(document.getElementById('conversion-report').textContent),
        replayPreferences: JSON.parse(document.getElementById('conversion-replay-preferences').textContent),
        replaySession: null, replayPages: new Map(), replayIndex: 0, replayTimer: null, replayPlaying: false,
        siteTree: JSON.parse(document.getElementById('conversion-site-catalog').textContent),
        goals: JSON.parse(document.getElementById('conversion-configuration').textContent) || [],
        suggestedGoals: JSON.parse(document.getElementById('conversion-suggestions').textContent) || [],
        mapTilesUnavailable: false,
        connection: null, saving: false, selectedDay: null, calendarYear: new Date().getUTCFullYear(), visitorMap: null, mapMarkers: null
    };
    if (model.goals.length && !model.goals.some(function primaryGoal(goal) { return goal.Primary; })) model.goals[0].Primary = true;
    function node(tagName,text,className) {
        const createdElement = document.createElement(tagName);
        if (text !== undefined) createdElement.textContent = String(text);
        if (className) createdElement.className = className;
        return createdElement;
    }
    function empty(container) { container.append(node('p',label('analytics_no_data'),'text-muted')); }
    function details(title,expanded) {
        const disclosure = node('details'); disclosure.open = !!expanded; disclosure.append(node('summary',title)); return disclosure;
    }
    function table(headers,rows) {
        const wrapper = node('div',undefined,'table-responsive');
        const reportTable = node('table',undefined,'table table-sm');
        const heading = node('thead'); const headingRow = node('tr');
        for (const column of headers) headingRow.append(node('th',column)); heading.append(headingRow);
        const body = node('tbody');
        for (const reportRow of rows) { const row = node('tr'); for (const cell of reportRow) { const column = node('td'); if (cell instanceof Node) column.append(cell); else column.textContent = String(cell); row.append(column); } body.append(row); }
        reportTable.append(heading,body); wrapper.append(reportTable); return wrapper;
    }
    function renderFunnels() {
        const container = document.getElementById('conversion-funnels'); container.replaceChildren();
        if (!model.report.Funnels || !model.report.Funnels.length) { empty(container); return; }
        for (const funnel of model.report.Funnels) {
            const funnelContainer = details(funnel.Name + ' · ' + funnel.Rate + ' · ' + funnel.Completed + ' / ' + funnel.Users,funnel.Primary);
            const steps = node('div',undefined,'conversion-steps');
            for (const step of funnel.Steps || []) {
                const stepElement = node('div',undefined,'conversion-step');
                stepElement.append(node('strong',step.Name === 'Visitors' ? label('cx_visitors') : step.Name),node('div',step.Users + ' ' + label('cx_visitors')),
                    node('small',label('cx_transition') + ': ' + step.Transition));
                if (step.Drop) stepElement.append(node('div',label('cx_lost') + ': ' + step.Drop),node('small',label('cx_pending') + ': ' + step.Pending + ' · ' + label('cx_lost') + ': ' + step.Lost));
                steps.append(stepElement);
            }
            funnelContainer.append(steps);
            if (funnel.BiggestFrom) funnelContainer.append(node('p',label('cx_biggest_drop') + ': ' + funnel.BiggestFrom + ' → ' + funnel.BiggestTo + ' · ' + funnel.BiggestDrop + ' · ' + label('cx_pending') + ': ' + funnel.BiggestPending));
            container.append(funnelContainer);
        }
    }
    function renderPathBranch(branch,parentPage) {
        const branchElement = node('li');
        branchElement.append(node('strong',branch.Label),node('small',branch.Users + ' ' + label('cx_visitors') + ' · ' + branch.Share + ' · ' + label('cx_conversion') + ': ' + branch.Conversion + ' · ' + label('cx_exit') + ': ' + branch.ExitRate));
        const publishedLinks = (model.siteTree.Links || {})[parentPage];
        if (publishedLinks && branch.Label.startsWith('/') && branch.Label !== parentPage && !publishedLinks.includes(branch.Label)) branchElement.append(node('small',label('cx_unlinked_path'),'badge text-bg-warning'));
        const children = branch.Children || [];
        if (children.length) {
            const childTree = node('ul',undefined,'conversion-tree');
            for (const childBranch of children.slice(0,5)) childTree.append(renderPathBranch(childBranch,branch.Label));
            if (children.length > 5) { const extraBranches = details('+' + (children.length - 5)); const extraTree = node('ul',undefined,'conversion-tree'); for (const childBranch of children.slice(5)) extraTree.append(renderPathBranch(childBranch,branch.Label)); extraBranches.append(extraTree); childTree.append(extraBranches); }
            branchElement.append(childTree);
        }
        if (branch.Exits) branchElement.append(node('small','↳ ' + label('cx_exit') + ' · ' + branch.Exits));
        return branchElement;
    }
    function visitorCard(visitor) {
        const card = node('div'); card.append(node('strong',visitor.Address || label('cx_unknown_location')),
            node('div',[visitor.Country,visitor.City].filter(Boolean).join(' · ')),node('div',label('ax_source') + ': ' + (visitor.Source || 'direct')),
            node('div',label('cx_landing') + ': ' + visitor.Landing),node('small',visitor.Last ? new Date(visitor.Last).toLocaleString() : ''));
        card.style.overflowWrap = 'anywhere';
        return card;
    }
    function renderVisitorCards(visitors,container) {
        let selectedPage = 0;
        function renderPage() {
            container.replaceChildren();
            if (!visitors.length) { empty(container); return; }
            for (const visitor of visitors.slice(selectedPage*20,(selectedPage+1)*20)) container.append(visitorCard(visitor));
            if (visitors.length <= 20) return;
            const pagination = node('div',undefined,'d-flex gap-2 align-items-center mt-2');
            const previous = node('button','←','btn btn-outline-secondary btn-sm'); previous.type = 'button'; previous.disabled = selectedPage === 0;
            const next = node('button','→','btn btn-outline-secondary btn-sm'); next.type = 'button'; next.disabled = (selectedPage+1)*20 >= visitors.length;
            previous.addEventListener('click',function previousPage() { selectedPage--; renderPage(); });
            next.addEventListener('click',function nextPage() { selectedPage++; renderPage(); });
            pagination.append(previous,node('small',(selectedPage+1) + ' / ' + Math.ceil(visitors.length/20)),next); container.append(pagination);
        }
        renderPage();
    }
    function renderVisitorMap() {
        const container = document.getElementById('conversion-visitor-map');
        const status = document.getElementById('conversion-map-status');
        const visitors = (model.report.Locations || []).filter(function inSelectedPeriod(visitor) { return (visitor.Days || []).some(function inSelectedYear(day) { return day.startsWith(String(model.calendarYear) + '-') && (!model.selectedDay || day === model.selectedDay); }); });
        const list = document.getElementById('conversion-map-visitors'); list.replaceChildren();
        renderVisitorCards(visitors,list);
        if (!window.L) { status.textContent = label('cx_map_unavailable'); return; }
        if (!model.visitorMap) {
            model.visitorMap = window.L.map(container,{scrollWheelZoom:false}).setView([20,0],2);
            const tiles = window.L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png',{maxZoom:18,referrerPolicy:'origin',attribution:'&copy; <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noopener">OpenStreetMap contributors</a>'});
            tiles.on('tileerror',function tileUnavailable() { if (!model.mapTilesUnavailable) { model.mapTilesUnavailable = true; status.textContent += ' · ' + label('cx_map_unavailable'); } });
            tiles.addTo(model.visitorMap);
            model.mapMarkers = window.L.layerGroup().addTo(model.visitorMap);
        }
        model.mapMarkers.clearLayers();
        const clusters = new Map();
        let unknown = 0;
        for (const visitor of visitors) {
            if (!visitor.Known) { unknown++; continue; }
            const clusterKey = visitor.Latitude + ':' + visitor.Longitude;
            if (!clusters.has(clusterKey)) clusters.set(clusterKey,[]);
            clusters.get(clusterKey).push(visitor);
        }
        const bounds = [];
        for (const cluster of clusters.values()) {
            const firstVisitor = cluster[0];
            const point = [firstVisitor.Latitude,firstVisitor.Longitude];
            const marker = window.L.circleMarker(point,{radius:Math.min(24,7+Math.sqrt(cluster.length)*2),color:'#087f8c',fillColor:'#21b6a8',fillOpacity:0.7});
            marker.bindTooltip(cluster.length + ' ' + label('cx_visitors')); marker.bindPopup(function visitorPopup() { const popup = node('div'); renderVisitorCards(cluster,popup); return popup; }); marker.addTo(model.mapMarkers); bounds.push(point);
        }
        if (bounds.length === 1) model.visitorMap.setView(bounds[0],4);
        else if (bounds.length > 1) model.visitorMap.fitBounds(bounds,{padding:[24,24],maxZoom:5});
        status.textContent = (clusters.size ? visitors.length + ' ' + label('cx_visitors') : label('analytics_no_data')) + ' · ' + label('cx_unknown_location') + ': ' + unknown;
        if (model.mapTilesUnavailable) status.textContent += ' · ' + label('cx_map_unavailable');
    }
    function calendarActivityLevel(visitors,maximum) {
        if (visitors <= 0 || maximum <= 0) return 0;
        if (visitors >= maximum) return 4;
        return 1 + Math.floor((visitors - 1)*3/(maximum - 1));
    }
    function calendarActivityRanges(maximum) {
        const ranges = [{level:0,minimumVisitors:0,maximumVisitors:0}];
        for (let level = 1; level <= 4; level++) {
            const minimumVisitors = level === 4 ? maximum : Math.ceil((level-1)*Math.max(0,maximum-1)/3)+1;
            const maximumVisitors = level === 4 ? maximum : Math.ceil(level*Math.max(0,maximum-1)/3);
            if (maximum > 0 && minimumVisitors <= maximumVisitors) ranges.push({level,minimumVisitors,maximumVisitors});
        }
        return ranges;
    }
    function renderVisitCalendar() {
        const container = document.getElementById('conversion-visit-calendar'); container.replaceChildren();
        const counts = new Map(); const years = new Set([new Date().getUTCFullYear()]);
        for (const day of model.report.Daily || []) { counts.set(day.Date,day.Visitors); years.add(Number(day.Date.slice(0,4))); }
        const yearButtons = node('div',undefined,'d-flex gap-2 mb-2');
        for (const year of Array.from(years).sort().reverse()) {
            const yearButton = node('button',year,'btn btn-outline-secondary btn-sm'); yearButton.type = 'button'; yearButton.setAttribute('aria-pressed',String(year === model.calendarYear));
            yearButton.addEventListener('click',function selectYear() { model.calendarYear = year; model.selectedDay = null; renderVisitCalendar(); renderVisitorMap(); }); yearButtons.append(yearButton);
        }
        document.getElementById('conversion-calendar-years').replaceChildren(yearButtons);
        const selection = node('p',model.selectedDay ? model.selectedDay + ' · ' + (counts.get(model.selectedDay) || 0) + ' ' + label('cx_visitors') : label('cx_visitors'),'small'); container.append(selection);
        const grid = node('div',undefined,'activity-calendar-grid');
        const firstDay = new Date(Date.UTC(model.calendarYear,0,1)); const nextYear = new Date(Date.UTC(model.calendarYear+1,0,1));
        const firstWeekday = (firstDay.getUTCDay()+6)%7;
        const monthLabels = node('div',undefined,'activity-calendar-months');
        for (let month = 0; month < 12; month++) {
            const monthStart = new Date(Date.UTC(model.calendarYear,month,1));
            const monthLabel = node('span',new Intl.DateTimeFormat(document.documentElement.lang || 'en',{month:'short',timeZone:'UTC'}).format(monthStart));
            monthLabel.style.gridColumn = String(Math.floor((Math.round((monthStart-firstDay)/86400000)+firstWeekday)/7)+1); monthLabels.append(monthLabel);
        }
        container.append(monthLabels);
        let maximum = 0;
        for (const day of model.report.Daily || []) { if (day.Date.startsWith(String(model.calendarYear))) maximum = Math.max(maximum,day.Visitors); }
        for (let date = firstDay; date < nextYear; date = new Date(date.getTime()+86400000)) {
            const dayKey = date.toISOString().slice(0,10); const visitors = counts.get(dayKey) || 0;
            const dayIndex = Math.round((date-firstDay)/86400000); const dayButton = node('button',undefined,'activity-calendar-day');
            dayButton.type = 'button'; dayButton.dataset.level = String(calendarActivityLevel(visitors,maximum));
            dayButton.style.gridColumn = String(Math.floor((dayIndex+firstWeekday)/7)+1); dayButton.style.gridRow = String((date.getUTCDay()+6)%7+1);
            dayButton.title = dayKey + ' · ' + visitors + ' ' + label('cx_visitors'); dayButton.setAttribute('aria-label',dayButton.title); dayButton.setAttribute('aria-pressed',String(model.selectedDay === dayKey));
            dayButton.disabled = date > new Date();
            dayButton.addEventListener('click',function selectDay() { model.selectedDay = model.selectedDay === dayKey ? null : dayKey; renderVisitCalendar(); renderVisitorMap(); }); grid.append(dayButton);
        }
        const legend = node('div',undefined,'activity-calendar-legend d-flex flex-wrap gap-3 mt-2');
        for (const interval of calendarActivityRanges(maximum)) {
            const range = interval.minimumVisitors === interval.maximumVisitors ? String(interval.minimumVisitors) : interval.minimumVisitors + '–' + interval.maximumVisitors;
            const entry = node('span',undefined,'d-inline-flex align-items-center gap-1');
            const swatch = node('span',undefined,'activity-calendar-day'); swatch.dataset.level = String(interval.level); swatch.setAttribute('aria-hidden','true');
            entry.append(swatch,node('small',range)); entry.title = range + ' ' + label('cx_visitors'); legend.append(entry);
        }
        container.append(grid,legend,node('small',label('ax_activity_less') + ' → ' + label('ax_activity_more') + ' · ' + label('cx_visitors'),'text-muted'));
    }
    function renderSiteTreeBranch(branch,isRoot) {
        const branchElement = node('li');
        const disclosure = details(branch.Title === branch.Path ? branch.Path : branch.Title + ' · ' + branch.Path,isRoot);
        branchElement.append(disclosure);
        const traffic = branch.Traffic;
        if (traffic) disclosure.append(node('small',traffic.Users + ' ' + label('cx_visitors') + ' · ' + label('cx_entrances') + ': ' + traffic.Entrances + ' · ' + label('cx_conversion') + ': ' + traffic.Conversion + ' · ' + label('cx_exit') + ': ' + traffic.ExitRate));
        else if (branch.Published) disclosure.append(node('small',label('analytics_no_data'),'text-muted'));
        let expanded = false;
        function expandSiteBranch() {
            if (!disclosure.open || expanded) return;
            expanded = true;
            if (traffic && traffic.Children && traffic.Children.length) {
                const nextSteps = details(label('cx_next_steps'),true);
                const journeyTree = node('ul',undefined,'conversion-tree');
                for (const nextBranch of traffic.Children.slice(0,5)) journeyTree.append(renderPathBranch(nextBranch,branch.Path));
                if (traffic.Children.length > 5) {
                    const otherSteps = details('+' + (traffic.Children.length - 5));
                    const otherTree = node('ul',undefined,'conversion-tree');
                    for (const nextBranch of traffic.Children.slice(5)) otherTree.append(renderPathBranch(nextBranch,branch.Path));
                    otherSteps.append(otherTree); journeyTree.append(otherSteps);
                }
                nextSteps.append(journeyTree); disclosure.append(nextSteps);
            }
            if (branch.Children && branch.Children.length) {
                const publishedTree = node('ul',undefined,'conversion-tree');
                for (const childPage of branch.Children) publishedTree.append(renderSiteTreeBranch(childPage,false));
                disclosure.append(publishedTree);
            }
        }
        disclosure.addEventListener('toggle',expandSiteBranch);
        expandSiteBranch();
        return branchElement;
    }
    function renderSiteTree() {
        const container = document.getElementById('conversion-site-tree'); container.replaceChildren();
        if (model.siteTree.Unavailable) { container.append(node('p',label('cx_tree_unavailable'),'text-muted')); return; }
        if (model.siteTree.Truncated) container.append(node('p',label('cx_tree_partial'),'text-muted'));
        if (!model.siteTree.Root) { empty(container); return; }
        const siteTree = node('ul',undefined,'conversion-tree'); siteTree.append(renderSiteTreeBranch(model.siteTree.Root,true)); container.append(siteTree);
    }
    function renderPaths() {
        const container = document.getElementById('conversion-paths'); container.replaceChildren();
        if (!model.report.Paths || !model.report.Paths.length) { empty(container); return; }
        for (const root of model.report.Paths) { const rootDetails = details(root.Root,root === model.report.Paths[0]); const tree = node('ul',undefined,'conversion-tree'); tree.append(renderPathBranch(root,root.Root)); rootDetails.append(tree); container.append(rootDetails); }
    }
    function showHeatmap(page,container) {
        const frame = node('iframe',undefined,'conversion-heatmap-frame');
        frame.setAttribute('sandbox','allow-same-origin'); frame.setAttribute('title',label('cx_preview') + ' ' + page.Page); frame.loading = 'lazy';
        const previewURL = new URL(location.pathname,location.origin); previewURL.searchParams.set('analytics',''); previewURL.searchParams.set('preview',page.Page);
        frame.src = previewURL.pathname + previewURL.search;
        frame.addEventListener('load',function previewLoaded() {
            const previewDocument = frame.contentDocument;
            if (!previewDocument || previewDocument.documentElement.dataset.analyticsLayout !== page.Layout) { container.prepend(node('p',label('cx_preview_changed'),'text-muted')); return; }
            previewDocument.addEventListener('click',function preventNavigation(clickEvent) { clickEvent.preventDefault(); },true);
            const observations = new Map(); let maximum = 1;
            for (const element of page.Elements || []) { if (element.Kind === 'click') { observations.set(element.Element,element.Clicks); maximum = Math.max(maximum,element.Clicks); } }
            for (const previewElement of previewDocument.querySelectorAll('[data-preview-key]')) {
                const clickCount = observations.get(previewElement.dataset.previewKey);
                if (!clickCount) continue;
                const intensity = Math.max(0.15,clickCount / maximum * 0.75);
                previewElement.style.backgroundColor = 'rgba(255,60,0,' + intensity + ')';
                previewElement.style.outline = '2px solid rgba(255,60,0,0.6)';
                previewElement.setAttribute('title',label('cx_clicks') + ': ' + clickCount);
            }
        });
        container.append(frame);
    }
    function renderBehaviour() {
        const container = document.getElementById('conversion-behaviour'); container.replaceChildren();
        if (!model.report.Behaviour || !model.report.Behaviour.length) { empty(container); return; }
        for (const page of model.report.Behaviour) {
            const pageDetails = details(page.Page + ' · ' + page.Device + ' · ' + page.Visitors + ' ' + label('cx_visitors'));
            const clickDetails = details(label('cx_clicks'));
            const previewButton = node('button',label('cx_preview'),'btn btn-outline-secondary btn-sm'); previewButton.type = 'button';
            const previewContainer = node('div');
            previewButton.addEventListener('click',function openPreview() { previewButton.disabled = true; showHeatmap(page,previewContainer); });
            clickDetails.append(previewButton,previewContainer,table([label('cx_element'),label('cx_event'),label('cx_visitors'),label('cx_clicks')],(page.Elements || []).map(function elementRow(element) { return [element.Element,element.Kind === 'dead-click' ? label('cx_dead') : element.Kind === 'rage-click' ? label('cx_rage') : label('cx_clicks'),element.Users,element.Clicks]; })));
            pageDetails.append(clickDetails);
            const scrollDetails = details(label('cx_scroll'));
            for (const depth of page.Scroll || []) { const scrollRow = node('div',undefined,'conversion-scroll-row'); const progress = node('progress'); progress.max = 100; progress.value = parseFloat(depth.Rate) || 0; progress.setAttribute('aria-label',depth.Depth + '%'); scrollRow.append(node('span',depth.Depth + '%'),progress,node('span',depth.Rate)); scrollDetails.append(scrollRow); }
            if (page.Sections && page.Sections.length) scrollDetails.append(table([label('cx_element'),label('cx_visitors'),label('cx_rate')],page.Sections.map(function sectionRow(section) { return [section.Element,section.Users,section.Rate]; })));
            pageDetails.append(scrollDetails); container.append(pageDetails);
        }
    }
    function renderForms() {
        const container = document.getElementById('conversion-forms'); container.replaceChildren();
        if (!model.report.Forms || !model.report.Forms.length) { empty(container); return; }
        for (const form of model.report.Forms) {
            const formDetails = details(form.Page + ' · ' + form.Form);
            formDetails.append(table([label('cx_form_viewed'),label('cx_form_started'),label('cx_form_submit'),label('cx_form_success'),label('cx_form_failed')],[[form.Viewed,form.Started,form.Submitted,form.Completed,form.Failed]]));
            formDetails.append(node('p',label('cx_field_drop') + ': ' + form.Abandoned));
            if (form.Fields && form.Fields.length) formDetails.append(table([label('cx_element'),label('cx_visitors')],form.Fields.map(function fieldRow(field) { return [field.Label,field.Count]; })));
            container.append(formDetails);
        }
    }
    function referrerLink(referrer) {
        if (!referrer) return '—';
        try {
            const destination = new URL(referrer);
            if (destination.protocol !== 'http:' && destination.protocol !== 'https:') return referrer;
            const link = node('a',referrer); link.href = destination.href; link.rel = 'noopener noreferrer'; link.target = '_blank'; return link;
        } catch (referrerError) { console.error('SiteBrush analytics: invalid referrer',referrerError.name); return referrer; }
    }
    function analyticsCommand(command,onReply,status) {
        const contact = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/_sitebrush/analytics');
        let finished = false;
        contact.addEventListener('open',function submitCommand() { command.csrf=dashboard.dataset.csrf; contact.send(JSON.stringify(command)); });
        contact.addEventListener('message',function receiveReply(messageEvent) {
            try { const response=JSON.parse(messageEvent.data); finished=true; if (response.error) status.textContent=label('cx_save_error')+': '+response.error; else onReply(response); }
            catch (replyError) { status.textContent=label('cx_save_error');console.error('SiteBrush analytics: command reply',replyError.name); }
            contact.close();
        });
        contact.addEventListener('error',function contactFailed() { status.textContent=label('cx_save_error'); });
        contact.addEventListener('close',function contactClosed() { if (!finished) status.textContent=label('cx_save_error'); });
    }
    function renderReplaySettings() {
        const container=document.getElementById('conversion-replay-settings');container.replaceChildren();
        const enabledLabel=node('label',label('cx_replay_enabled'),'d-flex gap-2 align-items-center');const enabled=node('input');enabled.type='checkbox';enabled.checked=model.replayPreferences.enabled;
        enabled.addEventListener('change',function replayEnabled() { model.replayPreferences.enabled=enabled.checked; });enabledLabel.prepend(enabled);container.append(enabledLabel);
        container.append(inputField('cx_replay_sample',String(model.replayPreferences.sample),function replaySample(sampleText) { model.replayPreferences.sample=Number(sampleText); },'number'));
        container.append(button('ax_save',function saveReplayPreferences() {
            const status=document.getElementById('conversion-replay-status');status.textContent='';
            analyticsCommand({operation:'configure-replay',replay:model.replayPreferences},function preferencesSaved() { status.textContent=label('cx_saved'); },status);
        }));
    }
    function renderSessions() {
        const container=document.getElementById('conversion-sessions');container.replaceChildren();
        if (!model.report.Sessions || !model.report.Sessions.length) { empty(container);return; }
        for (const session of model.report.Sessions) {
            const summary=node('div',undefined,'border-bottom py-2');summary.append(node('small',new Date(session.Started).toLocaleString()+' · '+session.Events+' · '+(session.Reasons || []).join(', ')));
            if (session.Limited) summary.append(node('small',label('cx_replay_limited')));
            summary.append(button('cx_replay_play',function openReplay() {
                const status=document.getElementById('conversion-replay-status');status.textContent='';
                analyticsCommand({operation:'replay-session',session:session.ID},function replayLoaded(response) {
                    if (model.replayTimer) window.clearTimeout(model.replayTimer);model.replayPlaying=false;model.replaySession=response.session;model.replayIndex=0;model.replayPages=new Map();
                    for (const page of response.pages || []) model.replayPages.set(page.Path+'\x1f'+page.Layout,page);renderReplayPlayer();
                },status);
            }));container.append(summary);
        }
    }
    function renderReplayPlayer() {
        const container=document.getElementById('conversion-replay-player');container.replaceChildren();
        if (!model.replaySession) return;
        const actions=model.replaySession.Events || [];
        const status=node('p',undefined,'small');const frame=node('iframe',undefined,'conversion-heatmap-frame');frame.setAttribute('sandbox','allow-same-origin');frame.title=label('cx_sessions');
        const playback=node('button',label('cx_replay_play'),'btn btn-outline-secondary btn-sm');playback.type='button';
        const timeline=node('ol',undefined,'small');const viewport=node('div');viewport.style.overflow='hidden';
        let shownPage='';
        function renderAction() {
            const action=actions[model.replayIndex];if (!action) return;
            status.textContent=(model.replayIndex+1)+' / '+actions.length+' · '+action.Path+' · '+action.kind;
            const pageKey=action.Path+'\x1f'+action.Layout;
            const snapshot=model.replayPages.get(pageKey);
            if (shownPage!==pageKey) { shownPage=pageKey;frame.srcdoc=snapshot && snapshot.HTML ? snapshot.HTML : '<!doctype html><body></body>'; }
            if (!snapshot || !snapshot.HTML) status.textContent+=' · '+label('cx_replay_snapshot');
            let recordedWidth=container.clientWidth;let recordedHeight=480;
            for (const previousAction of actions.slice(0,model.replayIndex+1)) { if (previousAction.width) recordedWidth=previousAction.width;if (previousAction.height) recordedHeight=previousAction.height; }
            const scale=Math.min(1,Math.max(1,container.clientWidth)/Math.max(1,recordedWidth));frame.style.width=recordedWidth+'px';frame.style.height=recordedHeight+'px';frame.style.transformOrigin='top left';frame.style.transform='scale('+scale+')';viewport.style.height=recordedHeight*scale+'px';
            const documentPreview=frame.contentDocument;if (!documentPreview) return;
            for (const previewElement of documentPreview.querySelectorAll('[data-preview-key]')) previewElement.style.outline=previewElement.getAttribute('data-preview-key')===action.element ? '3px solid #f97316' : '';
            if (action.kind==='scroll') frame.contentWindow.scrollTo(0,Math.max(0,documentPreview.documentElement.scrollHeight-frame.clientHeight)*(action.scroll || 0)/100);
            for (const eventRow of timeline.children) eventRow.style.fontWeight=Number(eventRow.dataset.index)===model.replayIndex ? 'bold' : '';
        }
        function scheduleAction() {
            if (!model.replayPlaying || model.replayIndex>=actions.length-1) { model.replayPlaying=false;playback.textContent=label('cx_replay_play');return; }
            const delay=Math.min(2000,Math.max(100,new Date(actions[model.replayIndex+1].Time)-new Date(actions[model.replayIndex].Time)));
            model.replayTimer=window.setTimeout(function nextAction() { model.replayIndex++;renderAction();scheduleAction(); },delay);
        }
        playback.addEventListener('click',function togglePlayback() {
            model.replayPlaying=!model.replayPlaying;if (model.replayTimer) window.clearTimeout(model.replayTimer);
            playback.textContent=label(model.replayPlaying ? 'cx_replay_pause':'cx_replay_play');if (model.replayPlaying) { if (model.replayIndex>=actions.length-1) model.replayIndex=0;renderAction();scheduleAction(); }
        });
        for (let eventIndex=0;eventIndex<actions.length;eventIndex++) {
            const action=actions[eventIndex];const eventRow=node('li');eventRow.dataset.index=String(eventIndex);const jump=node('button',new Date(action.Time).toLocaleTimeString()+' · '+action.kind+' · '+action.Path,'btn btn-link btn-sm');jump.type='button';
            jump.addEventListener('click',function selectAction() { model.replayIndex=eventIndex;renderAction(); });eventRow.append(jump);timeline.append(eventRow);
        }
        frame.addEventListener('load',renderAction);const eventsDetails=details(label('cx_optional'));eventsDetails.append(timeline);viewport.append(frame);container.append(playback,status,viewport,eventsDetails);renderAction();
    }
    function renderPageImpacts() {
        const container=document.getElementById('conversion-page-impact');container.replaceChildren();container.append(node('p',label('cx_impact_hint'),'small'));
        if (!model.report.Impacts || !model.report.Impacts.length) { empty(container);return; }
        for (const impact of model.report.Impacts) {
            const change=details(impact.Page+' · '+new Date(impact.At).toLocaleDateString()+' · '+(impact.Relative || '—'));
            if (impact.BeforeLabel && impact.AfterLabel) change.append(node('p','“'+impact.BeforeLabel+'” → “'+impact.AfterLabel+'”'));
            change.append(table([label('cx_before'),label('cx_after')],[[impact.BeforeRate+' · '+impact.BeforeCompleted+' / '+impact.BeforeVisitors,impact.AfterRate+' · '+impact.AfterCompleted+' / '+impact.AfterVisitors]]));
            if (impact.SmallSample) change.append(node('small',label('cx_impact_small')));if (impact.Partial) change.append(node('small',label('cx_impact_partial')));if (impact.OtherChanges) change.append(node('small',label('cx_impact_other')));container.append(change);
        }
    }
    function renderSources() {
        const container = document.getElementById('conversion-sources'); container.replaceChildren();
        const sources = model.report.Sources || [];
        if (!sources.length) { empty(container); return; }
        function sourceRows(sourceList) { return sourceList.map(function sourceRow(source) { return [source.Campaign ? source.Name+' · '+source.Campaign : source.Name,source.Visitors,source.Rate]; }); }
        container.append(table([label('ax_source'),label('cx_visitors'),label('cx_conversion')],sourceRows(sources.slice(0,5))));
        const sourceDetails = details(label('cx_optional'));
        sourceDetails.append(table([label('ax_source'),label('cx_visitors'),label('cx_conversion'), 'UTM', 'Referrer',label('ax_page')],(model.report.Referrers || sources).map(function sourceRow(source) { return [source.Name,source.Visitors,source.Rate,source.Campaign,referrerLink(source.Referrer),source.Landing]; })));
        container.append(sourceDetails);
    }
    function renderCohorts() {
        const container = document.getElementById('conversion-cohorts'); container.replaceChildren();
        if (!model.report.Cohorts || !model.report.Cohorts.length) { empty(container); return; }
        function cohortTable(cohorts,weekCount) {
            const headings = [label('cx_week'),label('cx_visitors')]; for (let weekIndex = 1; weekIndex <= weekCount; weekIndex++) headings.push(label('cx_week') + ' ' + weekIndex);
            return table(headings,cohorts.map(function cohortRow(cohort) { return [cohort.Week,cohort.Visitors,...cohort.Weeks.slice(0,weekCount).map(function retentionWeek(week) { return week.Rate + (week.Eligible && week.Partial ? ' *' : ''); })]; }));
        }
        container.append(cohortTable(model.report.Cohorts.slice(0,3),3));
        const cohortDetails = details(label('cx_optional')); cohortDetails.append(cohortTable(model.report.Cohorts,12)); container.append(cohortDetails);
    }
    function renderInsights() {
        const container = document.getElementById('conversion-insights'); container.replaceChildren();
        for (const insight of model.report.Insights || []) {
            const insightText = label('cx_insight_' + insight.Kind).replace('{page}',insight.Subject).replace('{users}',String(insight.Users)).replace('{rate}',insight.Rate).replace('{source}',insight.Subject).replace('{other}',insight.Compared).replace('{factor}',insight.Factor);
            container.append(node('p',insightText,'alert alert-secondary'));
        }
    }
    function inputField(title,initialText,onChange,type) {
        const fieldLabel = node('label',label(title),'small'); const input = node('input',undefined,'form-control form-control-sm'); input.type = type || 'text'; input.value = initialText || ''; input.maxLength = 256;
        input.addEventListener('change',function updateField() { onChange(input.value); }); fieldLabel.append(input); return fieldLabel;
    }
    function button(title,onClick) { const actionButton = node('button',label(title),'btn btn-outline-secondary btn-sm'); actionButton.type = 'button'; actionButton.addEventListener('click',onClick); return actionButton; }
    const eventKinds = ['page','click','scroll','download','form-start','form-submit','form-success','external-link','event','uri','action'];
    function goalRulesSignature(goal) {
        const steps = goal.Steps && goal.Steps.length ? goal.Steps : [goal];
        return JSON.stringify(steps.map(function stepRules(step) {
            return [step.Kind === 'path' ? 'uri' : step.Kind || 'page',step.Page || '',step.Element || '',step.Match || '',step.Kind === 'scroll' ? Number(step.Threshold || 50) : 0,step.SuccessPage || ''];
        }));
    }
    function renderGoals() {
        const container = document.getElementById('conversion-goals'); container.replaceChildren();
        document.getElementById('conversion-add-goal').disabled = model.goals.length >= 8;
        document.getElementById('conversion-suggest-goals').disabled = model.goals.length >= 8 || model.suggestedGoals.every(function alreadyConfigured(suggestion) { return model.goals.some(function sameRules(goal) { return goalRulesSignature(goal) === goalRulesSignature(suggestion); }); });
        for (const goal of model.goals) {
            const goalEditor = node('div',undefined,'conversion-editor');
            if (goal.Automatic) goalEditor.append(node('p',label('cx_automatic_goal'),'small text-muted'));
            const nameRow = node('div',undefined,'conversion-editor-row');
            nameRow.append(inputField('cx_name',goal.Name,function renameGoal(goalName) { goal.Name = goalName; }));
            const primaryLabel = node('label',label('cx_primary')); const primaryInput = node('input'); primaryInput.type = 'radio'; primaryInput.name = 'conversion-primary'; primaryInput.checked = !!goal.Primary;
            primaryInput.addEventListener('change',function choosePrimary() { for (const candidateGoal of model.goals) candidateGoal.Primary = candidateGoal === goal; renderGoals(); }); primaryLabel.prepend(primaryInput); nameRow.append(primaryLabel);
            nameRow.append(button('cx_remove',function removeGoal() { model.goals = model.goals.filter(function keepGoal(candidateGoal) { return candidateGoal !== goal; }); renderGoals(); })); goalEditor.append(nameRow);
            if (!goal.Steps || !goal.Steps.length) goal.Steps = [{Name:goal.Name,Kind:goal.Kind || 'page',Match:goal.Match || '',Page:goal.Page || '',Element:goal.Element || '',Threshold:goal.Threshold || 50}];
            for (const step of goal.Steps) {
                const stepRow = node('div',undefined,'conversion-editor-row');
                stepRow.append(inputField('cx_step',step.Name,function renameStep(stepName) { step.Name = stepName; }));
                const kindLabel = node('label',label('cx_event'),'small'); const kindSelect = node('select',undefined,'form-select form-select-sm');
                for (const kind of eventKinds) { const kindOption = node('option',label('cx_kind_' + kind.replaceAll('-','_'))); kindOption.value = kind; kindOption.selected = kind === (step.Kind === 'path' ? 'uri' : step.Kind); kindSelect.append(kindOption); }
                kindSelect.addEventListener('change',function changeKind() { step.Kind = kindSelect.value; renderGoals(); }); kindLabel.append(kindSelect); stepRow.append(kindLabel);
                stepRow.append(inputField('ax_page',step.Page,function changePage(pagePath) { step.Page = pagePath; }),inputField('cx_element',step.Element,function changeElement(elementKey) { step.Element = elementKey; }));
                if (step.Kind === 'form-success') stepRow.append(inputField('cx_success_page',step.SuccessPage,function changeSuccessPage(successPage) { step.SuccessPage = successPage; }));
                if (step.Kind === 'scroll') stepRow.append(inputField('cx_threshold',String(step.Threshold || 50),function changeThreshold(threshold) { step.Threshold = Number(threshold); },'number'));
                else if (['event','uri','action'].includes(step.Kind)) stepRow.append(inputField('cx_match',step.Match,function changeMatch(eventMatch) { step.Match = eventMatch; }));
                stepRow.append(button('cx_remove',function removeStep() { if (goal.Steps.length <= 1) return; goal.Steps = goal.Steps.filter(function keepStep(candidateStep) { return candidateStep !== step; }); renderGoals(); })); goalEditor.append(stepRow);
            }
            goalEditor.append(button('cx_add_step',function addStep() { if (goal.Steps.length >= 10) return; goal.Steps.push({Name:label('cx_step'),Kind:'page',Match:'',Page:'',Element:''}); renderGoals(); })); container.append(goalEditor);
        }
    }
    document.getElementById('conversion-suggest-goals').addEventListener('click',function addSuggestedGoals() {
        for (const suggestion of model.suggestedGoals) {
            if (model.goals.length >= 8) break;
            const duplicate = model.goals.some(function sameGoal(goal) { return goalRulesSignature(goal) === goalRulesSignature(suggestion); });
            if (duplicate) continue;
            const suggestedGoal = JSON.parse(JSON.stringify(suggestion));
            suggestedGoal.Primary = model.goals.length === 0;
            model.goals.push(suggestedGoal);
        }
        renderGoals();
    });
    document.getElementById('conversion-add-goal').addEventListener('click',function addGoal() { if (model.goals.length >= 8) return; model.goals.push({Name:label('ax_goals') + ' ' + (model.goals.length + 1),Primary:model.goals.length === 0,Steps:[{Name:label('cx_step'),Kind:'page',Page:'/',Match:'',Element:''}]}); renderGoals(); });
    document.getElementById('conversion-save').addEventListener('click',function saveConfiguration() {
        if (model.saving) return;
        model.saving = true;
        const status = document.getElementById('conversion-save-status'); status.textContent = '';
        model.connection = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/_sitebrush/analytics');
        model.connection.addEventListener('open',function sendConfiguration() { model.connection.send(JSON.stringify({operation:'configure',csrf:dashboard.dataset.csrf,goals:model.goals})); });
        model.connection.addEventListener('message',function configurationSaved(messageEvent) {
            const response = JSON.parse(messageEvent.data); status.textContent = response.error ? label('cx_save_error') + ': ' + response.error : label('cx_saved'); model.saving = false; model.connection.close();
        });
        model.connection.addEventListener('error',function saveFailed() { status.textContent = label('cx_save_error'); model.saving = false; });
        model.connection.addEventListener('close',function saveClosed() { if (model.saving) { status.textContent = label('cx_save_error'); model.saving = false; } });
    });
    renderFunnels(); renderGoals(); renderSiteTree(); renderPaths(); renderBehaviour(); renderForms(); renderReplaySettings(); renderSessions(); renderPageImpacts(); renderSources(); renderCohorts(); renderInsights(); renderVisitCalendar(); renderVisitorMap();
})();
