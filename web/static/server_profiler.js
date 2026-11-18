(function installServerProfiler() {
 'use strict';
 const root = document.querySelector('[data-profiler]');
 if (!root) return;
 const model = {
  role:'application', roleSelected:false, targets:[], selectedRun:'', overviewParts:{}, overviewKey:'', exportText:'', exportKey:'', exportRevision:0, copyStatus:'', copyFallback:false, view:'functions', page:0, search:'', selected:'', focus:0,
  sort:'total', ascending:false, kind:'', minimum:0, unit:'percent',
  expanded:0, detail:null, detailPending:false, detailError:'',
  detailsRevision:0, reportRevision:0, states:{}, report:null, more:false,
  error:'', pendingRoles:{}, stopped:true, refreshing:false,
  pollTimer:null, searchTimer:null, generation:0, cancelPendingRoles:{}
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

 // Each process owns one response handler; a failed contact cannot stall another process.
 function request(role,command) {
  return new Promise(function enqueue(resolve,reject) {
   if(model.stopped){reject(new Error('Страница приостановлена'));return;}
   queue.push({role:role,command:command,resolve:resolve,reject:reject});
   drain(role);
  });
 }
 function drain(role) {
  if(model.pendingRoles[role]||model.stopped)return;
  const queuedIndex=queue.findIndex(function sameProcess(queued){return queued.role===role;});
  if(queuedIndex<0)return;
  model.pendingRoles[role]=true;
  const job=queue.splice(queuedIndex,1)[0];
  const generation=model.generation;
  let settled=false;
  let socket=sockets[job.role];
  function fail(message) {
   if(settled)return;
   settled=true;
   window.clearTimeout(deadline);
   if(generation===model.generation) {
    model.pendingRoles[role]=false;
    delete model.cancelPendingRoles[role];
   }
   job.reject(new Error(message));
   if(generation===model.generation)drain(role);
  }
  const deadline=window.setTimeout(function closeUnanswered() {
   if(socket)socket.close();
   fail('Истекло время ожидания профилировщика');
  },role==='public'?30000:10000);
  model.cancelPendingRoles[role]=function abandonJob(){fail('Связь со страницей приостановлена');};
  function send() {
   if(settled||generation!==model.generation)return;
   socket.onmessage=function receive(event) {
    if(settled||generation!==model.generation)return;
    window.clearTimeout(deadline);
    settled=true;
    delete model.cancelPendingRoles[role];
    socket.onclose=function idleDisconnected() {
     if(sockets[job.role]===socket)sockets[job.role]=null;
    };
    model.pendingRoles[role]=false;
    try {
     const response=JSON.parse(event.data);
     model.states[job.role]=response;
     job.resolve(response);
    } catch(error) {
     job.reject(error);
    }
    drain(role);
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
 function availableMeasurements() {
  const measurements=new Map();
  model.targets.forEach(function processReports(role) {
   const state=model.states[role]||{};
   (state.reports||[]).forEach(function record(report) {
    const identifier=report.runID||report.id;
    if(!measurements.has(identifier))measurements.set(identifier,{id:identifier,started:report.started,parts:{}});
    measurements.get(identifier).parts[role]=report;
   });
  });
  return Array.from(measurements.values()).sort(function newest(first,second){return Date.parse(second.started)-Date.parse(first.started);});
 }
 function measurementPart(role) {
  const measurement=availableMeasurements().find(function selected(record){return record.id===model.selectedRun;});
  return measurement&&measurement.parts[role];
 }
 function processLabel(role) {
  if(role==='public')return 'Приём запросов и отдача файлов';
  if(model.targets.length===1)return 'Сайт и приложение в одном процессе';
  return 'Приложение: вход, редактор и база';
 }
 function measurementKey() {
  return model.selectedRun+model.targets.map(function part(role) {
   const record=measurementPart(role);
   return role+':'+(record?record.id:'missing')+':'+((model.states[role]||{}).error||'');
  }).join('|');
 }
 function updateMeasurements() {
  if(model.stopped)return;
  const records=availableMeasurements();
  const capturing=model.targets.some(function active(role){const state=model.states[role]||{};return state.busy&&state.runID===model.selectedRun;});
  if(!records.some(function selected(record){return record.id===model.selectedRun;})&&!capturing) {
   model.selectedRun=records.length?records[0].id:'';
   resetDetails();
  }
  if(!model.roleSelected&&model.targets.includes('public')&&measurementPart('public')&&model.role!=='public') {
   model.role='public';model.page=0;model.report=null;resetDetails();
  }
  if(!measurementPart(model.role)) {
   const availableRole=model.targets.find(function hasReport(role){return Boolean(measurementPart(role));});
   if(availableRole&&availableRole!==model.role){model.role=availableRole;model.page=0;model.report=null;resetDetails();}
  }
  if(measurementKey()!==model.overviewKey)loadOverview();
  else if(!model.exportKey&&model.selectedRun)prepareExport();
  render();
 }
 function refresh() {
  if(model.stopped||model.refreshing||!model.targets.length)return;
  model.refreshing=true;
  window.clearTimeout(model.pollTimer);
  const generation=model.generation;
  Promise.all(model.targets.map(function readRole(role) {
   return request(role,{action:'status'}).then(function compatible(state) {
    if(model.stopped||generation!==model.generation)return;
    if(!state.error&&state.protocol!==1) {
     state.error='Эта часть сервера использует старый протокол профилирования. Нужен полный перезапуск службы после обновления.';
     state.reports=[];
     state.incompatible=true;
    }
    updateMeasurements();
   }).catch(function unavailable(error) {
    if(model.stopped||generation!==model.generation)return;
    model.states[role]={role:role,error:error.message,reports:[]};
    updateMeasurements();
   });
  })).catch(function failedRefresh(error){if(generation===model.generation)reportError(error);}).finally(function nextRefresh() {
   if(generation!==model.generation)return;
   model.refreshing=false;
   if(!model.stopped)model.pollTimer=window.setTimeout(refresh,2000);
  });
 }
 function loadOverview() {
  const generation=model.generation;
  const key=measurementKey();
  model.overviewKey=key;
  model.overviewParts={};
  model.exportText='';
  model.exportKey='';
  model.copyStatus='';
  model.copyFallback=false;
  const expected=model.targets.filter(function available(role){return Boolean(measurementPart(role));});
  // Process statistics arrive independently; only the combined export needs all overviews.
  expected.forEach(function overview(role) {
   request(role,{action:'report',id:measurementPart(role).id,view:'overview'}).then(function received(response){return {role:role,report:response.report,error:response.error};}).catch(function unavailable(error){return {role:role,error:error.message};}).then(function loaded(part) {
    if(model.stopped||generation!==model.generation||key!==measurementKey())return;
    model.overviewParts[part.role]=part.report||{error:part.error||'Результат недоступен',role:part.role};
    const selectedOverview=model.overviewParts[model.role];
    if(selectedOverview&&selectedOverview.error) {
     const availableRole=model.targets.find(function hasOverview(processRole){const processReport=model.overviewParts[processRole];return processReport&&processReport.pid&&!processReport.error;});
     if(availableRole){model.role=availableRole;model.page=0;model.report=null;resetDetails();loadReport();}
    }
    if(expected.every(function completed(processRole){return Boolean(model.overviewParts[processRole]);}))prepareExport();
    render();
   }).catch(function failedOverview(error){if(generation===model.generation&&key===measurementKey()){model.overviewKey='';reportError(error);}});
  });
  loadReport();
 }
 function overviewReport() {
  const summary={cpu:0,heap:0,allocated:0,gc:0,groups:[],functions:[],stages:[],network:null,truncated:false};
  const counted=new Set();
  const groups=new Map();
  model.targets.forEach(function include(role) {
   const report=model.overviewParts[role];
   if(!report||report.error||counted.has(report.pid))return;
   counted.add(report.pid);
   summary.cpu+=report.cpu||0;
   summary.heap+=report.heap||0;
   summary.allocated+=report.allocated||0;
   summary.gc+=report.gc||0;
   summary.truncated=summary.truncated||report.truncated;
   (report.groups||[]).forEach(function group(portion){groups.set(portion.kind,(groups.get(portion.kind)||0)+portion.self);});
   (report.functions||[]).forEach(function functionCost(functionReport){summary.functions.push(Object.assign({},functionReport,{processRole:role}));});
   (report.stages||[]).forEach(function stage(stageReport){summary.stages.push(Object.assign({},stageReport,{processRole:role}));});
  });
  groups.forEach(function group(self,kind){summary.groups.push({kind:kind,self:self});});
  const external=model.overviewParts[model.targets.includes('public')?'public':'application'];
  if(external&&!external.error)summary.network=external.network;
  summary.functions.sort(function hottest(first,second){return second.self-first.self;});
  summary.stages.sort(function slowest(first,second){return second.meanMS-first.meanMS;});
  return summary;
 }
 function prepareExport() {
  if(!model.selectedRun||model.stopped)return;
  const key=measurementKey();
  if(model.exportKey===key)return;
  model.exportKey=key;
  const revision=++model.exportRevision;
  const generation=model.generation;
  const summary=overviewReport();
  const intro=[
   '# Отчёт о работе SiteBrush под внешней нагрузкой',
   'Цель анализа: найти узкие места и архитектурные причины медленной отдачи статики; предложить изменения кода на основании измерений.',
   'Замер: '+model.selectedRun,
   'Измерены процессы, обслуживающие сайт. Служебный запускатель (если используется) не обслуживает HTTP-нагрузку и не включён в запись.',
   'По измеренным процессам: CPU '+duration(summary.cpu)+'; живая память Go '+formatBytes(summary.heap)+'; выделено '+formatBytes(summary.allocated)+'; сборок мусора '+summary.gc+'.',
   'Внешних HTTP-запросов: '+(summary.network?summary.network.requests:'нет данных')+'. Данные внутренних границ ниже нельзя складывать с внешними запросами и трафиком.',
   'Результаты ab и nginx не измеряются этим отчётом; для сравнения приложите их отдельно.',
   'Это выборочные измерения CPU и памяти; наблюдения не доказывают причин задержек.'
  ];
  Promise.all(model.targets.map(function exportProcess(role) {
   const record=measurementPart(role);
   if(!record)return Promise.resolve('## '+processLabel(role)+'\nДанные отсутствуют: '+((model.states[role]||{}).error||'результат не получен или ещё готовится')+'\n');
   return request(role,{action:'export',id:record.id}).then(function exported(response){return response.text||'## '+processLabel(role)+'\nОшибка: '+(response.error||'нет текста')+'\n';}).catch(function unavailable(error){return '## '+processLabel(role)+'\nДанные отсутствуют: '+error.message+'\n';});
  })).then(function exported(parts) {
   if(model.stopped||generation!==model.generation||revision!==model.exportRevision||key!==measurementKey())return;
   model.exportText=intro.join('\n')+'\n\n'+parts.join('\n\n');
   render();
  }).catch(function failedExport(error) {
   if(generation!==model.generation||revision!==model.exportRevision)return;
   model.exportKey='';
   model.copyStatus='Не удалось подготовить отчёт: '+error.message;
   render();
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
  const selectedPart=measurementPart(model.role);
  model.selected=selectedPart?selectedPart.id:"";
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
  appendStatistic(summary,'Внешних HTTP-запросов',report.network?report.network.requests:'Нет данных');
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
 function stageLabel(name) {
  return {accepted_to_request:'Приём соединения → запрос',front_state:'Ожидание состояния публичного сервера',domain:'Проверка домена',policy:'Политика доступа',attack_guard:'Защита от атак',throttle_guard:'Ограничение нагрузки',public_handler:'Публичная обработка до первого ответа',first_write:'До первого ответа',certificate:'TLS-сертификат',attack_queue:'Очередь защиты от атак',attack_work:'Работа защиты от атак',throttle_queue:'Очередь ограничения нагрузки',throttle_work:'Работа ограничения нагрузки',proxy_metadata:'Метаданные прокси',proxy_backend:'Ожидание приложения за прокси',handler_complete:'Завершение обработчика',body_write:'Вызовы записи тела',http1_response_complete:'HTTP/1 keep-alive: завершение ответа после flush'}[name]||name;
 }
 function renderStages(content,report) {
  const output=table(['Этап обработки','Наблюдений','Среднее, мс','p90 ≤ мс','p95 ≤ мс','p99 ≤ мс']);
  const body=document.createElement('tbody');
  (report.stages||[]).slice().sort(function slowest(first,second){return second.meanMS-first.meanMS;}).forEach(function stage(stageReport) {
   const row=document.createElement('tr');
   textCell(row,stageLabel(stageReport.name));
   textCell(row,stageReport.count,'profiler-number');
   textCell(row,stageReport.meanMS.toFixed(3),'profiler-number');
   ['p90MS','p95MS','p99MS'].forEach(function bound(key){textCell(row,typeof stageReport[key]==='number'?stageReport[key].toFixed(3):'—','profiler-number');});
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
 function renderOverview(summary) {
  const panel=element('[data-profiler-overview]');
  panel.replaceChildren();
  if(!model.targets.length){appendParagraph(panel,'Подключаем процессы сервера…');return;}
  const output=table(['Процесс','PID','Пользователь','CPU','Живая память','Выделено','GC','HTTP-запросы']);
  const body=document.createElement('tbody');
  model.targets.forEach(function processStatistics(role) {
   const processReport=model.overviewParts[role];
   const processState=model.states[role]||{};
   const row=document.createElement('tr');
   textCell(row,processLabel(role));
   if(processReport&&processReport.pid&&!processReport.error) {
    textCell(row,String(processReport.pid));
    textCell(row,processReport.user||'—');
    textCell(row,duration(processReport.cpu),'profiler-number');
    textCell(row,formatBytes(processReport.heap),'profiler-number');
    textCell(row,formatBytes(processReport.allocated),'profiler-number');
    textCell(row,String(processReport.gc||0),'profiler-number');
    textCell(row,processReport.network?String(processReport.network.requests):'Нет данных','profiler-number');
   } else {
    const status=textCell(row,processState.error||(processReport&&processReport.error)||(processState.protocol===1?'Подключён · нет записи':'Подключаем…'));
    status.setAttribute('colspan','7');
   }
   body.appendChild(row);
  });
  output.appendChild(body);
  panel.appendChild(output);
  if(model.targets.includes('public'))appendParagraph(panel,'Внешние HTTP-запросы учитываются публичным процессом. Запросы приложения за прокси показаны отдельно и не прибавляются к ним.');
  renderDiagnostics(panel);
  if(summary.truncated)appendParagraph(panel,'Часть измерений сокращена или пропущена под нагрузкой; это отмечено в отчёте.');
 }

 function ramPolicySummary(policy) {
  if(!policy)return 'Нет данных';
  return 'routing='+policy.routingVersion+' frozen='+policy.frozen+' versioned='+policy.versioned+' private='+policy.privateTestDrive+' pending='+policy.pendingRegistration+' stealth='+policy.stealth+'; fingerprints='+policy.preparedPages+'/'+policy.preparedFiles+'; root='+policy.rootPrepared+' index='+policy.indexPrepared+' published='+policy.rootPublished+'; IP admin/server='+policy.administratorIPRules+'/'+policy.serverIPRules;
 }
 function renderDiagnostics(parent) {
  const reports=model.targets.map(function participant(role){return model.overviewParts[role];}).filter(Boolean);
  if(!reports.some(function available(report){return report.ramCache||report.runtime;}))return;
  const heading=document.createElement('h3');heading.textContent='Готовые RAM-ответы и планировщик Go';parent.appendChild(heading);
  const output=table(['Процесс','RAM / бюджет','Тел','Попаданий / промахов','Пик runnable','Пик waiting','Планировщик p95 ≤ мс']);
  const body=document.createElement('tbody');
  model.targets.forEach(function participant(role) {
   const report=model.overviewParts[role];if(!report||report.error)return;
   const row=document.createElement('tr');textCell(row,processLabel(role));
   const cache=report.ramCache;
   textCell(row,cache?(cache.limit?formatBytes(cache.bytes)+' / '+formatBytes(cache.limit):'Выключен'):'Нет данных');
   textCell(row,cache?String(cache.bodies):'—');
   textCell(row,cache?cache.hits+' / '+cache.misses:'—');
   const samples=report.runtime&&report.runtime.samples||[];
   ['/sched/goroutines/runnable:goroutines','/sched/goroutines/waiting:goroutines'].forEach(function peak(name) {
    const counts=samples.map(function count(sample){return sample.counts&&sample.counts[name];}).filter(function available(count){return typeof count==='number';});
    textCell(row,counts.length?String(Math.max(...counts)):'Нет данных');
   });
   const scheduler=report.runtime&&report.runtime.scheduler;
   textCell(row,scheduler&&typeof scheduler.p95MS==='number'?scheduler.p95MS.toFixed(3):'Нет данных');
   body.appendChild(row);
  });
  output.appendChild(body);parent.appendChild(output);
  model.targets.forEach(function cacheDiagnostics(role) {
   const report=model.overviewParts[role];const cache=report&&report.ramCache;if(!cache)return;
   const reasons=Object.keys(cache.missReasons||{}).sort();
   if(reasons.length)appendParagraph(parent,processLabel(role)+': RAM misses — '+reasons.map(function describe(reason){return (reason||'unknown')+': '+cache.missReasons[reason];}).join(', '));
   const sites=cache.sites||[];if(!sites.length)return;
   const siteTable=table(['Домен','Маршрутов в RAM','Варианты /','Причина отсутствия вариантов','Политика публикации','Восстановлено страниц / файлов']);
   const siteBody=document.createElement('tbody');
   sites.forEach(function siteDiagnostics(site){const row=document.createElement('tr');textCell(row,site.domain);textCell(row,String(site.routes));textCell(row,(site.rootVariants||[]).join(', ')||'—');textCell(row,site.rootMiss||'—');textCell(row,ramPolicySummary(site.policy));textCell(row,typeof site.recoveredPages==='number'?site.recoveredPages+' / '+site.recoveredFiles+(site.rootRecovered?' (включая /)':''):'Нет данных');siteBody.appendChild(row);});
   siteTable.appendChild(siteBody);parent.appendChild(siteTable);
  });
  appendParagraph(parent,'RAM учитывает подготовку и используемые поколения, отдельно от RSS. Счётчики goroutine приблизительные. Подробные отсчёты доступны во вкладке Goroutine.');
 }
 function renderRuntime(content,report) {
  if(!report.runtime)return;
  const heading=document.createElement('h3');heading.textContent='Планировщик Go во время записи';content.appendChild(heading);
  const output=table(['Время, мс','Всего goroutine','Выполняются','Готовы к выполнению','Ожидают','В syscall/cgo','GOMAXPROCS']);
  const body=document.createElement('tbody');
  (report.runtime.samples||[]).forEach(function sample(record) {
   const row=document.createElement('tr');textCell(row,String(record.elapsedMS));
   ['/sched/goroutines:goroutines','/sched/goroutines/running:goroutines','/sched/goroutines/runnable:goroutines','/sched/goroutines/waiting:goroutines','/sched/goroutines/not-in-go:goroutines','/sched/gomaxprocs:threads'].forEach(function metric(name){const count=record.counts&&record.counts[name];textCell(row,typeof count==='number'?String(count):'Нет данных');});
   body.appendChild(row);
  });
  output.appendChild(body);content.appendChild(output);
  appendParagraph(content,'Значения получены раз в секунду через runtime/metrics. Отсутствующая метрика обозначается явно; приблизительные счётчики не обязаны складываться в общее число.');
 }

 function render() {
  const report=model.report;
  const summary=overviewReport();
  const history=element('[data-profiler-history]');
  history.replaceChildren();
  availableMeasurements().forEach(function option(record) {
   const choice=document.createElement('option');
   choice.value=record.id;
   choice.textContent=new Date(record.started).toLocaleTimeString()+' · замер сервера';
   choice.selected=record.id===model.selectedRun;
   history.appendChild(choice);
  });
  const busy=model.targets.map(function state(role){return model.states[role]||{};}).filter(function capturing(state){return state.busy;});
  const elapsed=busy.length?Math.min(30,Math.max(0,Math.floor((Date.now()-Date.parse(busy[0].started))/1000))):0;
  element('[data-profiler-progress]').value=elapsed;
  element('[data-profiler-progress]').hidden=!busy.length;
  element('[data-profiler-status]').textContent=busy.length?'Записываем работу сервера… осталось '+(30-elapsed)+' с.':'Готов к записи';
  const processErrors=model.targets.map(function failed(role){const state=model.states[role]||{};return state.error?processLabel(role)+': '+state.error:'';}).filter(Boolean);
  const error=model.error||processErrors.join('; ');
  element('[data-profiler-error]').textContent=error;
  element('[data-profiler-error]').hidden=!error;
  element('[data-profiler-start]').disabled=model.stopped||!model.targets.length||Boolean(busy.length);
  element('[data-profiler-cancel]').disabled=!busy.length;
  element('[data-profiler-delete]').disabled=!model.selectedRun||Boolean(busy.length);
  element('[data-profiler-copy]').disabled=!model.exportText||Boolean(busy.length);
  element('[data-profiler-copy-status]').textContent=model.copyStatus||(!busy.length&&model.selectedRun&&!model.exportText?'Готовим текстовый отчёт…':'');
  element('[data-profiler-copy-fallback]').hidden=!model.copyFallback;
  element('[data-profiler-copy-text]').value=model.exportText;
  element('[data-profiler-mode]').textContent=model.targets.length>1?'Замер охватывает отдачу файлов посетителям и работу приложения. Выбирать процессы вручную не нужно.':'Один процесс обслуживает сайт и приложение; замер охватывает его работу целиком.';
  const roles=element('[data-profiler-role]');
  roles.replaceChildren();
  model.targets.forEach(function option(role) {
   const choice=document.createElement('option');
   choice.value=role;
   choice.textContent=processLabel(role);
   choice.selected=model.role===role;
   roles.appendChild(choice);
  });
  const process=model.overviewParts[model.role];
  element('[data-profiler-process]').textContent=process&&process.pid?'PID '+process.pid+' · '+process.user+' · '+process.system+'/'+process.architecture+' · SiteBrush '+process.version:'Результат этой части сервера ещё не получен';
  element('[data-profiler-role]').value=model.role;
  element('[data-profiler-search]').value=model.search;
  element('[data-profiler-kind]').value=model.kind;
  element('[data-profiler-minimum]').value=String(model.minimum);
  element('[data-profiler-unit]').value=model.unit;
  element('[data-profiler-minimum]').disabled=model.view==='memory';
  element('[data-profiler-unit]').disabled=model.view==='memory';
  element('[data-profiler-filters]').hidden=!tabularView();
  element('[data-profiler-pagination]').hidden=!tabularView()||!report;
  renderSummary(model.selectedRun?summary:null);
  renderOverview(summary);
  root.querySelectorAll('[data-view]').forEach(function tab(button){button.setAttribute('aria-selected',String(button.dataset.view===model.view));});
  const content=element('[data-profiler-content]');
  content.replaceChildren();
  if(!report){renderEmpty(content);return;}
  if(model.view==='graph')renderGraph(content,report);
  else if(model.view==='network')renderNetwork(content,report);
  else if(model.view==='goroutines') {
   const output=document.createElement('pre');
   output.textContent=report.goroutines||'Нет данных';
   content.appendChild(output);
   renderRuntime(content,report);
  } else if(model.view==='stages')renderStages(content,report);
  else renderFunctions(content,report);
  element('[data-profiler-count]').textContent='Функций по фильтру: '+(report.matches||0);
  element('[data-profiler-page]').textContent='Страница '+(model.page+1);
  element('[data-profiler-back]').disabled=model.page===0;
  element('[data-profiler-next]').disabled=!model.more;
 }
 element('[data-profiler-start]').onclick=function start() {
  model.error='';
  model.copyStatus='';
  model.exportText='';
  request('application',{action:'start'}).then(function applicationStarted(state) {
   if(state.error)throw new Error(state.error);
   model.selectedRun=state.runID;
   if(model.targets.includes('public')) {
    if((model.states.public||{}).incompatible)return {error:'Для записи отдачи файлов нужен полный перезапуск службы после обновления.'};
    return request('public',{action:'start',runID:state.runID});
   }
  }).then(function started(state) {
   if(state&&state.error)model.error='Запись части сервера недоступна: '+state.error;
   return refresh();
  }).catch(reportError);
 };
 element('[data-profiler-cancel]').onclick=function cancel() {
  Promise.all(model.targets.map(function stop(role){return request(role,{action:'cancel'});})).then(refresh).catch(reportError);
 };
 element('[data-profiler-delete]').onclick=function remove() {
  Promise.all(model.targets.map(function removePart(role) {
   const record=measurementPart(role);
   return record?request(role,{action:'delete',id:record.id}):Promise.resolve();
  })).then(function deleted(){model.selectedRun='';model.overviewKey='';model.overviewParts={};model.exportText='';model.exportKey='';model.exportRevision++;resetDetails();refresh();}).catch(reportError);
 };
 element('[data-profiler-role]').onchange=function role(event) {
  model.role=event.target.value;
  model.roleSelected=true;
  model.report=null;
  resetDetails();
  loadReport();
 };
 element('[data-profiler-history]').onchange=function select(event) {
  model.selectedRun=event.target.value;
  model.page=0;
  model.report=null;
  resetDetails();
  loadOverview();
 };
 element('[data-profiler-copy]').onclick=async function copyReport() {
  if(!model.exportText)return;
  const text=model.exportText;
  const generation=model.generation;
  try {
   await navigator.clipboard.writeText(text);
   if(model.stopped||generation!==model.generation||model.exportText!==text)return;
   model.copyStatus='Отчёт скопирован. Его можно вставить в ИИ для анализа.';
   model.copyFallback=false;
  } catch(error) {
   if(model.stopped||generation!==model.generation||model.exportText!==text)return;
   model.copyStatus='Браузер запретил копирование. Скопируйте выделенный текст вручную.';
   model.copyFallback=true;
  }
  render();
  if(model.copyFallback){element('[data-profiler-copy-text]').focus();element('[data-profiler-copy-text]').select();}
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
  Object.values(model.cancelPendingRoles).forEach(function abandonPending(cancel){cancel();});
  queue.splice(0).forEach(function abandonQueued(job){job.reject(new Error('Страница приостановлена'));});
  model.generation++;
  model.pendingRoles={};
  model.refreshing=false;
  model.cancelPendingRoles={};
  resetDetails();
  model.overviewParts={};model.overviewKey='';model.exportText='';model.exportKey='';model.exportRevision++;model.copyFallback=false;model.report=null;
  model.selectedRun='';model.selected='';model.states={};
  element('[data-profiler-copy-text]').value='';
  element('[data-profiler-overview]').replaceChildren();
  element('[data-profiler-content]').replaceChildren();
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
  model.selectedRun='';
  model.report=null;
  model.more=false;
  model.page=0;
  model.targets=[];
  model.roleSelected=false;
  const generation=model.generation;
  request('application',{action:'capabilities'}).then(function capabilities(state) {
   if(model.stopped||generation!==model.generation)return;
   if(state.error||!state.targets||!state.targets.length)throw new Error(state.error||'Сервер не сообщил режим профилирования');
   model.targets=state.targets;
   model.role=state.targets.includes('public')?'public':'application';
   refresh();
   render();
  }).catch(function unavailable(error){if(generation===model.generation)reportError(error);});
  render();
 }
 element('[data-profiler-open]').onclick=function openDialog(){element('[data-profiler-dialog]').showModal();resumeProfiler();};
 element('[data-profiler-close]').onclick=function closeDialog(){element('[data-profiler-dialog]').close();};
 element('[data-profiler-dialog]').addEventListener('close',suspendProfiler);
 window.addEventListener('pagehide',suspendProfiler);
 window.addEventListener('pageshow',resumeProfiler);
 render();
})();
