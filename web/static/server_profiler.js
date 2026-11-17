(function installServerProfiler() {
 'use strict';
 const root = document.querySelector('[data-profiler]');
 if (!root) return;
 const model = {
  role:'public', view:'functions', page:0, search:'', selected:'', focus:0,
  sort:'total', ascending:false, kind:'', minimum:0, unit:'percent',
  expanded:0, detail:null, detailPending:false, detailError:'',
  detailsRevision:0, reportRevision:0, states:{}, report:null, more:false,
  error:'', pending:false, stopped:true, refreshing:false,
  pollTimer:null, searchTimer:null, generation:0, cancelPending:null
 };
 const sockets = {};
 const queue = [];

 function element(selector) {
  return root.querySelector(selector);
 }
 function formatBytes(count) {
  return ((count||0)/1048576).toFixed(1)+' МиБ';
 }
 function duration(count) {
  return ((count||0)/1e6).toFixed(2)+' мс';
 }

 // One queued command owns the response handler; generations invalidate suspended-page replies.
 function request(role,command) {
  return new Promise(function enqueue(resolve,reject) {
   if(model.stopped){reject(new Error('Страница приостановлена'));return;}
   queue.push({role:role,command:command,resolve:resolve,reject:reject});
   drain();
  });
 }
 function drain() {
  if(model.pending||!queue.length||model.stopped)return;
  model.pending=true;
  const job=queue.shift();
  const generation=model.generation;
  let settled=false;
  let socket=sockets[job.role];
  function fail(message) {
   if(settled)return;
   settled=true;
   window.clearTimeout(deadline);
   if(generation===model.generation) {
    model.pending=false;
    model.cancelPending=null;
   }
   job.reject(new Error(message));
   if(generation===model.generation)drain();
  }
  const deadline=window.setTimeout(function closeUnanswered() {
   if(socket)socket.close();
   fail('Истекло время ожидания профилировщика');
  },10000);
  model.cancelPending=function abandonJob(){fail('Связь со страницей приостановлена');};
  function send() {
   if(settled||generation!==model.generation)return;
   socket.onmessage=function receive(event) {
    if(settled||generation!==model.generation)return;
    window.clearTimeout(deadline);
    settled=true;
    model.cancelPending=null;
    socket.onclose=function idleDisconnected() {
     if(sockets[job.role]===socket)sockets[job.role]=null;
    };
    model.pending=false;
    try {
     const response=JSON.parse(event.data);
     model.states[job.role]=response;
     job.resolve(response);
    } catch(error) {
     job.reject(error);
    }
    drain();
   };
   socket.onclose=function disconnected() {
    if(sockets[job.role]===socket)sockets[job.role]=null;
    fail('Связь с профилировщиком прервана');
   };
   socket.send(JSON.stringify(Object.assign({},job.command,{role:job.role,csrf:root.dataset.csrf})));
  }
  if(socket&&socket.readyState===WebSocket.OPEN){send();return;}
  socket=new WebSocket((location.protocol==='https:'?'wss:':'ws:')+'//'+location.host+'/?server_profiler_ws');
  sockets[job.role]=socket;
  socket.onopen=send;
  socket.onerror=function connectionError() {
   socket.close();
   fail('Профилировщик недоступен');
  };
  socket.onclose=function openingFailed() {
   if(sockets[job.role]===socket)sockets[job.role]=null;
   fail('Не удалось подключиться');
  };
 }
 function reportError(error) {
  if(model.stopped)return;
  model.error=error.message;
  render();
 }
 function refresh() {
  if(model.stopped||model.refreshing)return;
  model.refreshing=true;
  window.clearTimeout(model.pollTimer);
  const generation=model.generation;
  Promise.all(['public','application'].map(function readRole(role) {
   return request(role,{action:'status'});
  })).then(function updated() {
   if(model.stopped||generation!==model.generation)return;
   const state=model.states[model.role];
   if(state&&state.reports&&state.reports.length&&!state.reports.some(function matches(report){return report.id===model.selected;})) {
    model.selected=state.reports[0].id;
    model.page=0;
    resetDetails();
    loadReport();
   }
   if(state&&(!state.reports||!state.reports.length)) {
    model.selected='';
    model.report=null;
    resetDetails();
   }
   render();
  }).catch(function failedRefresh(error) {
   if(generation===model.generation)reportError(error);
  }).finally(function nextRefresh() {
   if(generation!==model.generation)return;
   model.refreshing=false;
   window.clearTimeout(model.pollTimer);
   if(!model.stopped)model.pollTimer=window.setTimeout(refresh,2000);
  });
 }
 function tabularView() {
  return ['functions','code','memory'].includes(model.view);
 }
 function resetDetails() {
  model.detailsRevision++;
  model.expanded=0;
  model.detail=null;
  model.detailPending=false;
  model.detailError='';
 }
 function loadReport() {
  if(!model.selected){model.report=null;render();return;}
  const selection=model.selected;
  const role=model.role;
  const generation=model.generation;
  const revision=++model.reportRevision;
  const command={action:'report',id:selection,view:model.view,page:model.page,search:model.search,sort:model.sort,ascending:model.ascending,kind:model.kind,minimum:model.minimum};
  request(role,command).then(function loaded(response) {
   if(model.stopped||generation!==model.generation||revision!==model.reportRevision||selection!==model.selected||role!==model.role)return;
   model.report=response.report||null;
   model.more=Boolean(response.more);
   model.error=response.error||'';
   model.focus=0;
   render();
  }).catch(function failedReport(error) {
   if(generation===model.generation&&revision===model.reportRevision)reportError(error);
  });
 }
 function openFunction(identifier) {
  const selection=model.selected;
  const role=model.role;
  const generation=model.generation;
  const revision=++model.detailsRevision;
  model.expanded=identifier;
  model.detail=null;
  model.detailError='';
  model.detailPending=true;
  render();
  request(role,{action:'report',id:selection,view:'function',function:identifier}).then(function loadedFunction(response) {
   if(model.stopped||generation!==model.generation||revision!==model.detailsRevision||selection!==model.selected||role!==model.role)return;
   model.detail=response.report&&response.report.detail;
   model.detailPending=false;
   model.detailError=response.error||(!model.detail?'Функция недоступна в этой записи':'');
   render();
  }).catch(function failedFunction(error) {
   if(generation!==model.generation||revision!==model.detailsRevision)return;
   model.detailPending=false;
   model.detailError=error.message;
   render();
  });
 }
 function textCell(row,text,className) {
  const cell=document.createElement('td');
  cell.textContent=text;
  if(className)cell.className=className;
  row.appendChild(cell);
  return cell;
 }
 function table(headers) {
  const output=document.createElement('table');
  const heading=document.createElement('thead');
  const headingRow=document.createElement('tr');
  headers.forEach(function title(label) {
   const cell=document.createElement('th');
   cell.textContent=label;
   headingRow.appendChild(cell);
  });
  heading.appendChild(headingRow);
  output.appendChild(heading);
  return output;
 }
 function appendParagraph(parent,text) {
  const paragraph=document.createElement('p');
  paragraph.textContent=text;
  parent.appendChild(paragraph);
 }
 function categoryLabel(kind) {
  return {application:'SiteBrush',runtime:'Go runtime',standard:'Стандартная библиотека',library:'Зависимости'}[kind]||'Другие';
 }
 function cpuCost(nanoseconds,total) {
  if(model.unit==='percent')return (100*(nanoseconds||0)/(total||1)).toFixed(2)+'%';
  if(model.unit==='us')return ((nanoseconds||0)/1000).toFixed(2)+' мкс';
  return duration(nanoseconds);
 }
 function costCell(row,cost,total,kind,memory) {
  const cell=textCell(row,'','profiler-number');
  const wrapper=document.createElement('div');
  wrapper.className='profiler-cost profiler-kind-'+kind;
  const caption=document.createElement('span');
  caption.textContent=memory?formatBytes(cost):cpuCost(cost,total);
  wrapper.appendChild(caption);
  const track=document.createElement('span');
  track.className='profiler-cost-track';
  const fill=document.createElement('span');
  fill.style.width=Math.max(0,Math.min(100,100*(cost||0)/(total||1)))+'%';
  track.appendChild(fill);
  wrapper.appendChild(track);
  cell.appendChild(wrapper);
 }
 function functionCell(row,functionReport,expandable) {
  const cell=textCell(row,'','profiler-function-cell');
  const marker=document.createElement('span');
  marker.className='profiler-kind-dot profiler-kind-'+functionReport.kind;
  marker.title=categoryLabel(functionReport.kind);
  cell.appendChild(marker);
  if(expandable) {
   const button=document.createElement('button');
   button.type='button';
   button.className='profiler-function-link';
   button.textContent=(model.expanded===functionReport.id?'▾ ':'▸ ')+functionReport.name;
   button.setAttribute('aria-expanded',String(model.expanded===functionReport.id));
   button.onclick=function toggleFunction() {
    if(model.expanded===functionReport.id){resetDetails();render();return;}
    openFunction(functionReport.id);
   };
   cell.appendChild(button);
  } else {
   const name=document.createElement('span');
   name.textContent=functionReport.name;
   cell.appendChild(name);
  }
  const locationLabel=document.createElement('small');
  locationLabel.className='profiler-source';
  locationLabel.textContent=(functionReport.file||'Без исходного файла')+(functionReport.line?':'+functionReport.line:'');
  cell.appendChild(locationLabel);
  return cell;
 }
 function sortHeader(heading,title,column) {
  const cell=document.createElement('th');
  cell.setAttribute('aria-sort',model.sort===column?(model.ascending?'ascending':'descending'):'none');
  const button=document.createElement('button');
  button.type='button';
  button.className='profiler-sort';
  button.textContent=title+(model.sort===column?(model.ascending?' ↑':' ↓'):' ↕');
  button.onclick=function changeSort() {
   model.ascending=model.sort===column?!model.ascending:column==='name';
   model.sort=column;
   model.page=0;
   loadReport();
  };
  cell.appendChild(button);
  heading.appendChild(cell);
 }
 function renderRelationships(parent,title,relationships,report) {
  const column=document.createElement('section');
  column.className='profiler-call-column';
  const heading=document.createElement('h4');
  heading.textContent=title;
  column.appendChild(heading);
  if(!relationships||!relationships.length) {
   appendParagraph(column,'Связей в выборке нет');
   parent.appendChild(column);
   return;
  }
  const output=table(['Функция','Образцы','CPU по связи']);
  const body=document.createElement('tbody');
  relationships.forEach(function relationship(functionReport) {
   const row=document.createElement('tr');
   const cell=functionCell(row,functionReport,false);
   const button=document.createElement('button');
   button.type='button';
   button.className='profiler-function-link';
   button.textContent='↳ Перейти';
   button.onclick=function followRelationship(){openFunction(functionReport.id);};
   cell.appendChild(button);
   textCell(row,functionReport.samples,'profiler-number');
   costCell(row,functionReport.total,report.cpu,functionReport.kind,false);
   body.appendChild(row);
  });
  output.appendChild(body);
  column.appendChild(output);
  parent.appendChild(column);
 }
 function renderDetails(parent,report) {
  const panel=document.createElement('section');
  panel.className='profiler-call-details';
  if(model.detailPending) {
   appendParagraph(panel,'Загрузка связей функции…');
  } else if(model.detailError) {
   appendParagraph(panel,model.detailError);
  } else if(model.detail) {
   const selected=model.detail.function;
   const heading=document.createElement('h3');
   heading.textContent=selected.name;
   panel.appendChild(heading);
   appendParagraph(panel,(selected.file||'Без исходного файла')+':'+selected.line+' · Собственное CPU: '+cpuCost(selected.self,report.cpu)+' · С вызовами: '+cpuCost(selected.total,report.cpu));
   const columns=document.createElement('div');
   columns.className='profiler-call-columns';
   renderRelationships(columns,'Вызывается из',model.detail.callers,report);
   renderRelationships(columns,'Вызывает',model.detail.callees,report);
   panel.appendChild(columns);
   appendParagraph(panel,'Затраты по связям получены из образцов стеков CPU. Рекурсивные связи могут пересекаться; их доли нельзя складывать.'+(model.detail.truncated?' Показаны первые 50 связей каждого направления.':''));
  }
  const close=document.createElement('button');
  close.type='button';
  close.textContent='Свернуть связи';
  close.onclick=function closeDetails(){resetDetails();render();};
  panel.appendChild(close);
  parent.appendChild(panel);
 }
 function renderFunctions(content,report) {
  const memory=model.view==='memory';
  const functions=memory?(report.memory||[]):(report.functions||[]);
  if(!functions.length) {
   appendParagraph(content,'Функций по выбранным условиям нет. Измените фильтр.');
   return;
  }
  if(model.expanded&&!functions.some(function expandedRow(functionReport){return functionReport.id===model.expanded;}))renderDetails(content,report);
  const output=document.createElement('table');
  const head=document.createElement('thead');
  const heading=document.createElement('tr');
  const rank=document.createElement('th');
  rank.textContent='#';
  heading.appendChild(rank);
  sortHeader(heading,'Функция / файл','name');
  if(model.view==='functions')sortHeader(heading,'Образцы','samples');
  sortHeader(heading,memory?'Собственная память':'Собственное CPU','self');
  sortHeader(heading,memory?'Память с вызовами':'CPU с вызовами','total');
  head.appendChild(heading);
  output.appendChild(head);
  const body=document.createElement('tbody');
  functions.forEach(function functionRow(functionReport,index) {
   const row=document.createElement('tr');
   if(model.expanded&&model.expanded===functionReport.id)row.className='profiler-row-selected';
   textCell(row,model.page*50+index+1,'profiler-rank');
   functionCell(row,functionReport,model.view==='functions');
   if(model.view==='functions')textCell(row,functionReport.samples,'profiler-number');
   costCell(row,functionReport.self,memory?report.heap:report.cpu,functionReport.kind,memory);
   costCell(row,functionReport.total,memory?report.heap:report.cpu,functionReport.kind,memory);
   body.appendChild(row);
   if(model.expanded&&model.expanded===functionReport.id) {
    const detailRow=document.createElement('tr');
    detailRow.className='profiler-detail-row';
    const detailCell=textCell(detailRow,'');
    detailCell.colSpan=5;
    renderDetails(detailCell,report);
    body.appendChild(detailRow);
   }
  });
  output.appendChild(body);
  content.appendChild(output);
 }
 function appendStatistic(parent,label,formatted) {
  const statistic=document.createElement('div');
  statistic.className='profiler-stat';
  statistic.textContent=label;
  const number=document.createElement('strong');
  number.textContent=formatted;
  statistic.appendChild(number);
  parent.appendChild(statistic);
 }
 function renderSummary(report) {
  const summary=element('[data-profiler-summary]');
  const breakdown=element('[data-profiler-breakdown]');
  summary.replaceChildren();
  breakdown.replaceChildren();
  if(!report)return;
  appendStatistic(summary,'CPU всего',duration(report.cpu));
  appendStatistic(summary,'Живая память',formatBytes(report.heap));
  appendStatistic(summary,'Выделено за запись',formatBytes(report.allocated));
  appendStatistic(summary,'Сборок мусора',report.gc);
  appendStatistic(summary,'HTTP-запросов',(report.network||{}).requests||0);
  const bar=document.createElement('div');
  bar.className='profiler-breakdown-bar';
  const groups=report.groups||[];
  groups.forEach(function segment(group) {
   const portion=document.createElement('span');
   portion.className='profiler-kind-'+group.kind;
   portion.style.width=Math.min(100,100*group.self/(report.cpu||1))+'%';
   portion.title=categoryLabel(group.kind)+' · '+duration(group.self);
   bar.appendChild(portion);
  });
  breakdown.appendChild(bar);
  groups.forEach(function legend(group) {
   const label=document.createElement('span');
   const marker=document.createElement('span');
   marker.className='profiler-kind-dot profiler-kind-'+group.kind;
   label.appendChild(marker);
   const caption=document.createElement('span');
   caption.textContent=categoryLabel(group.kind)+' '+(100*group.self/(report.cpu||1)).toFixed(1)+'%';
   label.appendChild(caption);
   breakdown.appendChild(label);
  });
 }
 function renderGraph(content,report){
  const frames=report.frames||[];if(!frames.length){content.textContent='Нет образцов CPU';return;}
  const focus=frames[model.focus]||frames[0];const descendants=new Set([model.focus]);const depth={};const offsets={};const placed={};depth[model.focus]=0;offsets[model.focus]=0;
  const svg=document.createElementNS('http://www.w3.org/2000/svg','svg');let height=30;
  frames.forEach(function draw(frame,index){
   if(index!==model.focus){if(!descendants.has(frame.parent))return;descendants.add(index);depth[index]=depth[frame.parent]+1;offsets[index]=offsets[frame.parent]+(placed[frame.parent]||0);placed[frame.parent]=(placed[frame.parent]||0)+frame.total;}
   const width=1000*frame.total/(focus.total||1);const left=1000*offsets[index]/(focus.total||1);const top=depth[index]*28;height=Math.max(height,top+28);
   const rectangle=document.createElementNS(svg.namespaceURI,'rect');rectangle.setAttribute('x',left);rectangle.setAttribute('y',top);rectangle.setAttribute('width',Math.max(width,0));rectangle.setAttribute('height',26);
   rectangle.setAttribute('fill',model.search && frame.name.toLowerCase().includes(model.search.toLowerCase())?'#ffcb60':'hsl('+(170+(depth[index]*17)%80)+' 55% '+(65+(index%3)*6)+'%)');
   const title=document.createElementNS(svg.namespaceURI,'title');title.textContent=frame.name+' · '+duration(frame.total);rectangle.appendChild(title);
   rectangle.addEventListener('click',function focusFrame(){model.focus=index;render();});svg.appendChild(rectangle);
   if(width>55){const label=document.createElementNS(svg.namespaceURI,'text');label.setAttribute('x',left+4);label.setAttribute('y',top+18);label.textContent=frame.name.slice(0,Math.floor(width/7));svg.appendChild(label);}
  });
  svg.setAttribute('viewBox','0 0 1000 '+height);svg.setAttribute('height',height);content.appendChild(svg);
  const detail=document.createElement('div');detail.className='profiler-detail';const parent=frames[focus.parent];detail.textContent='Функция: '+focus.name+' · Собственное: '+duration(focus.self)+' · С вызовами: '+duration(focus.total)+(parent?' · Вызвана из: '+parent.name:'')+' · Вызывает: '+frames.filter(function child(frame){return frame.parent===model.focus;}).slice(0,15).map(function name(frame){return frame.name;}).join(', ');content.appendChild(detail);
  const reset=document.createElement('button');reset.textContent='Весь граф';reset.onclick=function resetFocus(){model.focus=0;render();};content.appendChild(reset);
 }
 function renderNetwork(content,report) {
  const network=report.network||{};
  const output=document.createElement('div');
  output.className='profiler-network';
  appendStatistic(output,'HTTP-запросов',network.requests||0);
  appendStatistic(output,'Получено тел',formatBytes(network.received));
  appendStatistic(output,'Отправлено тел',formatBytes(network.sent));
  appendStatistic(output,'HTTP ошибок',network.errors||0);
  appendStatistic(output,'Ошибок записи',network.writeErrors||0);
  appendParagraph(output,'Учтены тела HTTP-запросов и ответов. Заголовки, TLS и повторные TCP-передачи не входят. Трафик публичного сервера и приложения нельзя складывать: проксируемые запросы проходят обе границы.');
  content.appendChild(output);
 }
 function renderStages(content,report) {
  const output=table(['Этап обработки','Запросов','Среднее, мс','p95 ≤ мс']);
  const body=document.createElement('tbody');
  const names={accepted_to_request:'Приём соединения → запрос',front_state:'Ожидание состояния публичного сервера',domain:'Проверка домена',policy:'Политика доступа',attack_guard:'Защита от атак',throttle_guard:'Ограничение нагрузки',public_handler:'Публичная обработка до первого ответа',first_write:'До первого ответа',certificate:'TLS-сертификат',attack_queue:'Очередь защиты от атак',attack_work:'Работа защиты от атак',throttle_queue:'Очередь ограничения нагрузки',throttle_work:'Работа ограничения нагрузки',proxy_metadata:'Метаданные прокси',proxy_backend:'Ожидание приложения за прокси'};
  (report.stages||[]).slice().sort(function slowest(first,second){return second.meanMS-first.meanMS;}).forEach(function stage(stageReport) {
   const row=document.createElement('tr');
   textCell(row,names[stageReport.name]||stageReport.name);
   textCell(row,stageReport.count,'profiler-number');
   textCell(row,stageReport.meanMS.toFixed(3),'profiler-number');
   textCell(row,stageReport.p95MS.toFixed(3),'profiler-number');
   body.appendChild(row);
  });
  output.appendChild(body);
  content.appendChild(output);
 }
 function renderEmpty(content) {
  const empty=document.createElement('div');
  empty.className='profiler-empty';
  const heading=document.createElement('strong');
  heading.textContent='Найдите, на что уходит время сервера';
  empty.appendChild(heading);
  appendParagraph(empty,'Запустите запись на 30 секунд и создайте нагрузку внешним ab. Затем откройте самые затратные функции и их связи.');
  content.appendChild(empty);
 }
 function render() {
  const state=model.states[model.role]||{};
  const report=model.report;
  const history=element('[data-profiler-history]');
  history.replaceChildren();
  (state.reports||[]).forEach(function option(record) {
   const choice=document.createElement('option');
   choice.value=record.id;
   choice.textContent=new Date(record.finished).toLocaleTimeString()+' · PID '+record.pid+' · v'+record.version;
   choice.selected=record.id===model.selected;
   history.appendChild(choice);
  });
  const elapsed=state.busy?Math.min(30,Math.max(0,Math.floor((Date.now()-Date.parse(state.started))/1000))):0;
  element('[data-profiler-progress]').value=elapsed;
  element('[data-profiler-progress]').hidden=!state.busy;
  element('[data-profiler-status]').textContent=state.busy?'Запись… осталось '+(30-elapsed)+' с.':'Готов к записи · '+(state.reports||[]).length+' временных записей'+(report&&report.truncated?' · Данные частичные':'');
  if(state.evicted)element('[data-profiler-status]').textContent+=' · Старейшие записи удалены по ограничению размера';
  const error=model.error||state.error||(report&&report.error)||'';
  element('[data-profiler-error]').textContent=error;
  element('[data-profiler-error]').hidden=!error;
  element('[data-profiler-start]').disabled=model.stopped||['public','application'].some(function busy(role){return model.states[role]&&model.states[role].busy;});
  element('[data-profiler-cancel]').disabled=!['public','application'].some(function busy(role){return model.states[role]&&model.states[role].busy;});
  element('[data-profiler-delete]').disabled=!model.selected;
  element('[data-profiler-role]').value=model.role;
  element('[data-profiler-search]').value=model.search;
  element('[data-profiler-kind]').value=model.kind;
  element('[data-profiler-minimum]').value=String(model.minimum);
  element('[data-profiler-unit]').value=model.unit;
  element('[data-profiler-minimum]').disabled=model.view==='memory';
  element('[data-profiler-unit]').disabled=model.view==='memory';
  element('[data-profiler-filters]').hidden=!tabularView();
  element('[data-profiler-pagination]').hidden=!tabularView()||!report;
  root.querySelectorAll('[data-view]').forEach(function tab(button){button.setAttribute('aria-selected',String(button.dataset.view===model.view));});
  renderSummary(report);
  const content=element('[data-profiler-content]');
  content.replaceChildren();
  if(!report){renderEmpty(content);return;}
  if(model.view==='graph')renderGraph(content,report);
  else if(model.view==='network')renderNetwork(content,report);
  else if(model.view==='goroutines') {
   const output=document.createElement('pre');
   output.textContent=report.goroutines||'Нет данных';
   content.appendChild(output);
  } else if(model.view==='stages')renderStages(content,report);
  else renderFunctions(content,report);
  element('[data-profiler-count]').textContent='Функций по фильтру: '+(report.matches||0);
  element('[data-profiler-page]').textContent='Страница '+(model.page+1);
  element('[data-profiler-back]').disabled=model.page===0;
  element('[data-profiler-next]').disabled=!model.more;
 }
 element('[data-profiler-start]').onclick=function start() {
  model.error='';
  Promise.all(['public','application'].map(function capture(role){return request(role,{action:'start'});})).then(refresh).catch(reportError);
 };
 element('[data-profiler-cancel]').onclick=function cancel() {
  Promise.all(['public','application'].map(function stop(role){return request(role,{action:'cancel'});})).then(refresh).catch(reportError);
 };
 element('[data-profiler-delete]').onclick=function remove() {
  request(model.role,{action:'delete',id:model.selected}).then(function deleted(){model.selected='';resetDetails();refresh();}).catch(reportError);
 };
 element('[data-profiler-role]').onchange=function role(event) {
  model.role=event.target.value;
  model.selected='';
  model.report=null;
  resetDetails();
  refresh();
 };
 element('[data-profiler-history]').onchange=function select(event) {
  model.selected=event.target.value;
  model.page=0;
  model.report=null;
  resetDetails();
  loadReport();
 };
 element('[data-profiler-search]').oninput=function search(event) {
  model.search=event.target.value;
  model.page=0;
  window.clearTimeout(model.searchTimer);
  model.searchTimer=window.setTimeout(loadReport,250);
 };
 element('[data-profiler-kind]').onchange=function category(event) {
  model.kind=event.target.value;
  model.page=0;
  loadReport();
 };
 element('[data-profiler-minimum]').onchange=function minimum(event) {
  model.minimum=Number(event.target.value);
  model.page=0;
  loadReport();
 };
 element('[data-profiler-unit]').onchange=function unit(event) {
  model.unit=event.target.value;
  render();
 };
 root.querySelectorAll('[data-view]').forEach(function tab(button) {
  button.onclick=function choose() {
   model.view=button.dataset.view;
   model.report=null;
   model.page=0;
   resetDetails();
   loadReport();
  };
 });
 element('[data-profiler-next]').onclick=function next(){model.page++;loadReport();};
 element('[data-profiler-back]').onclick=function previous(){model.page=Math.max(0,model.page-1);loadReport();};
 element('[data-profiler-expand]').onclick=function expand(){element('[data-profiler-dialog]').classList.toggle('profiler-fullscreen');};
 function suspendProfiler() {
  if(model.stopped)return;
  model.stopped=true;
  window.clearTimeout(model.pollTimer);
  window.clearTimeout(model.searchTimer);
  if(model.cancelPending)model.cancelPending();
  queue.splice(0).forEach(function abandonQueued(job){job.reject(new Error('Страница приостановлена'));});
  model.generation++;
  model.pending=false;
  model.refreshing=false;
  model.cancelPending=null;
  resetDetails();
  Object.keys(sockets).forEach(function disconnectRole(role) {
   const socket=sockets[role];
   sockets[role]=null;
   if(socket)socket.close();
  });
 }
 function resumeProfiler() {
  if(!model.stopped||!element('[data-profiler-dialog]').open)return;
  model.stopped=false;
  model.states={};
  model.error='';
  model.selected='';
  model.report=null;
  model.more=false;
  model.page=0;
  refresh();
  render();
 }
 element('[data-profiler-open]').onclick=function openDialog(){element('[data-profiler-dialog]').showModal();resumeProfiler();};
 element('[data-profiler-close]').onclick=function closeDialog(){element('[data-profiler-dialog]').close();};
 element('[data-profiler-dialog]').addEventListener('close',suspendProfiler);
 window.addEventListener('pagehide',suspendProfiler);
 window.addEventListener('pageshow',resumeProfiler);
 render();
})();
