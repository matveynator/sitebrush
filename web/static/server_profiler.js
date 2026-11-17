(function installServerProfiler() {
 'use strict';
 const root = document.querySelector('[data-profiler]');
 if (!root) return;
 const model = {role:'public',view:'functions',page:0,search:'',selected:'',focus:0,states:{},report:null,more:false,error:'',pending:false,stopped:false,refreshing:false,pollTimer:null,searchTimer:null,generation:0,cancelPending:null};
 const sockets = {};
 const queue = [];
 function element(selector) { return root.querySelector(selector); }
 function formatBytes(count) { return ((count || 0)/1048576).toFixed(1)+' МиБ'; }
 function duration(count) { return ((count || 0)/1e6).toFixed(2)+' мс'; }
 function request(role, command) {
  return new Promise(function enqueue(resolve,reject) {if(model.stopped){reject(new Error("Страница приостановлена"));return;}queue.push({role:role,command:command,resolve:resolve,reject:reject});drain();});
 }
 function drain() {
  if (model.pending || !queue.length || model.stopped) return;
  model.pending=true;
  const job=queue.shift();
  let settled=false;
  let deadline=null;
  const generation=model.generation;
  let socket=sockets[job.role];
  function fail(message) {if(settled)return;settled=true;window.clearTimeout(deadline);if(generation===model.generation){model.pending=false;model.cancelPending=null;}job.reject(new Error(message));if(generation===model.generation)drain();}
  model.cancelPending=function abandonJob(){fail('Связь со страницей приостановлена');};
  function send() {
   if(settled||generation!==model.generation)return;
   deadline=window.setTimeout(function closeUnanswered(){socket.close();},10000);
   socket.onmessage=function receive(event){
    if(settled||generation!==model.generation)return;
    window.clearTimeout(deadline);settled=true;model.cancelPending=null;socket.onclose=function idleDisconnected(){if(sockets[job.role]===socket)sockets[job.role]=null;};
    try {const response=JSON.parse(event.data);model.states[job.role]=response;model.pending=false;job.resolve(response);}catch(error){model.pending=false;job.reject(error);}
    drain();
   };
   socket.onclose=function disconnected(){window.clearTimeout(deadline);if(sockets[job.role]===socket)sockets[job.role]=null;fail('Связь с профилировщиком прервана');};
   socket.send(JSON.stringify(Object.assign({},job.command,{role:job.role,csrf:root.dataset.csrf})));
  }
  if (socket && socket.readyState===WebSocket.OPEN) {send();return;}
  socket=new WebSocket((location.protocol==='https:'?'wss:':'ws:')+'//'+location.host+'/?server_profiler_ws');sockets[job.role]=socket;
  socket.onopen=send;socket.onerror=function connectionError(){if(model.stopped||generation!==model.generation)return;model.error='Профилировщик недоступен';render();};socket.onclose=function openingFailed(){if(sockets[job.role]===socket)sockets[job.role]=null;fail('Не удалось подключиться');};
 }
 function reportError(error){if(model.stopped)return;model.error=error.message;render();}
 function refresh(){
  if(model.stopped||model.refreshing)return;
  model.refreshing=true;window.clearTimeout(model.pollTimer);
  const generation=model.generation;
  Promise.all(['public','application'].map(function readRole(role){return request(role,{action:'status'});})).then(function updated(){
   if(model.stopped||generation!==model.generation)return;
   const state=model.states[model.role];
   if(state && state.reports && state.reports.length && !state.reports.some(function matches(report){return report.id===model.selected;})){model.selected=state.reports[0].id;model.page=0;loadReport();}
   if(state && (!state.reports || !state.reports.length)){model.selected='';model.report=null;}
   render();
  }).catch(function failedRefresh(error){if(generation===model.generation)reportError(error);}).finally(function nextRefresh(){if(generation!==model.generation)return;model.refreshing=false;window.clearTimeout(model.pollTimer);if(!model.stopped)model.pollTimer=window.setTimeout(refresh,2000);});
 }
 function loadReport(){
  if(!model.selected){model.report=null;render();return;}
  const selection=model.selected;const role=model.role;const view=model.view;const generation=model.generation;
  request(role,{action:'report',id:selection,view:view,page:model.page,search:model.search}).then(function loaded(response){
   if(model.stopped||generation!==model.generation||selection!==model.selected||role!==model.role||view!==model.view)return;
   model.report=response.report||null;model.more=response.more;model.error=response.error||'';model.focus=0;render();
  }).catch(function failedReport(error){if(generation===model.generation)reportError(error);});
 }
 function textCell(row,text){const cell=document.createElement('td');cell.textContent=text;row.appendChild(cell);return cell;}
 function table(headers){const output=document.createElement('table');const heading=document.createElement('tr');headers.forEach(function title(label){const cell=document.createElement('th');cell.textContent=label;heading.appendChild(cell);});output.appendChild(heading);return output;}
 function showFunction(functionName){
  model.view='graph';model.search=functionName;
  loadReport();element('[data-profiler-search]').value=functionName;
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
 function render(){
  const state=model.states[model.role]||{};const report=model.report;
  const history=element('[data-profiler-history]');history.replaceChildren();(state.reports||[]).forEach(function option(record){const choice=document.createElement('option');choice.value=record.id;choice.textContent=new Date(record.finished).toLocaleTimeString()+' · PID '+record.pid+' · v'+record.version;choice.selected=record.id===model.selected;history.appendChild(choice);});
  element('[data-profiler-status]').textContent=state.busy?'Запись… осталось '+Math.max(0,30-Math.floor((Date.now()-Date.parse(state.started))/1000))+' с':'Готов к записи. '+((state.reports||[]).length)+' записей за последние 10 минут.';
  element('[data-profiler-error]').textContent=model.error||state.error||(report&&report.error)||'';
  if(state.evicted)element('[data-profiler-status]').textContent+=' Старейшие записи удалены по ограничению размера.';
  element('[data-profiler-start]').disabled=['public','application'].some(function busy(role){return model.states[role]&&model.states[role].busy;});
  element('[data-profiler-summary]').textContent=report?'CPU: '+duration(report.cpu)+' · Память: '+formatBytes(report.heap)+' · GC за запись: '+report.gc+(report.truncated?' · Данные частичные':''):'';
  root.querySelectorAll('[data-view]').forEach(function tab(button){button.setAttribute('aria-selected',String(button.dataset.view===model.view));});
  const content=element('[data-profiler-content]');content.replaceChildren();
  if(!report){content.textContent='Нажмите «Записать 30 секунд», затем создайте нагрузку на сайт.';return;}
  if(model.view==='graph'){renderGraph(content,report);}
  else if(model.view==='goroutines'){const output=document.createElement('pre');output.textContent=report.goroutines||'Нет данных';content.appendChild(output);}
  else if(model.view==='stages'){const output=table(['Стадия','Запросов','Среднее, мс','p95 ≤ мс']);(report.stages||[]).forEach(function stage(stageReport){const row=document.createElement('tr');textCell(row,stageReport.name);textCell(row,stageReport.count);textCell(row,stageReport.meanMS.toFixed(3));textCell(row,stageReport.p95MS.toFixed(3));output.appendChild(row);});content.appendChild(output);}
  else{const memory=model.view==='memory';const output=table(['Функция',memory?'Собственная память':'Собственное CPU',memory?'Память с вызовами':'CPU с вызовами','Доля']);(memory?report.memory||[]:report.functions||[]).filter(function search(functionReport){return functionReport.name.toLowerCase().includes(model.search.toLowerCase());}).forEach(function functionRow(functionReport){const row=document.createElement('tr');const cell=textCell(row,functionReport.name);cell.title=functionReport.file+':'+functionReport.line;cell.style.cursor='pointer';cell.onclick=function selectFunction(){showFunction(functionReport.name);};textCell(row,memory?formatBytes(functionReport.self):duration(functionReport.self));textCell(row,memory?formatBytes(functionReport.total):duration(functionReport.total));textCell(row,memory?'—':(100*functionReport.total/(report.cpu||1)).toFixed(1)+'%');output.appendChild(row);});content.appendChild(output);}
  element('[data-profiler-page]').textContent='Страница '+(model.page+1);element('[data-profiler-back]').disabled=model.page===0;element('[data-profiler-next]').disabled=!model.more||model.view!=='functions';
 }
 element('[data-profiler-start]').onclick=function start(){model.error='';Promise.all(['public','application'].map(function capture(role){return request(role,{action:'start'});})).then(refresh).catch(reportError);};
 element('[data-profiler-cancel]').onclick=function cancel(){Promise.all(['public','application'].map(function stop(role){return request(role,{action:'cancel'});})).then(refresh).catch(reportError);};
 element('[data-profiler-delete]').onclick=function remove(){request(model.role,{action:'delete',id:model.selected}).then(function deleted(){model.selected='';refresh();}).catch(reportError);};
 element('[data-profiler-role]').onchange=function role(event){model.role=event.target.value;model.selected='';model.report=null;refresh();};
 element('[data-profiler-history]').onchange=function select(event){model.selected=event.target.value;model.page=0;loadReport();};
 element('[data-profiler-search]').oninput=function search(event){model.search=event.target.value;if(model.view==='functions'){model.page=0;window.clearTimeout(model.searchTimer);model.searchTimer=window.setTimeout(loadReport,250);}else{render();}};
 root.querySelectorAll('[data-view]').forEach(function tab(button){button.onclick=function choose(){model.view=button.dataset.view;model.page=0;loadReport();};});
 element('[data-profiler-next]').onclick=function next(){model.page++;loadReport();};element('[data-profiler-back]').onclick=function previous(){model.page=Math.max(0,model.page-1);loadReport();};
 element('[data-profiler-expand]').onclick=function expand(){root.classList.toggle('profiler-fullscreen');};
 function suspendProfiler(){
  if(model.stopped)return;
  model.stopped=true;
  window.clearTimeout(model.pollTimer);window.clearTimeout(model.searchTimer);
  if(model.cancelPending)model.cancelPending();
  queue.splice(0).forEach(function abandonQueued(job){job.reject(new Error('Страница приостановлена'));});
  model.generation++;model.pending=false;model.refreshing=false;model.cancelPending=null;
  Object.keys(sockets).forEach(function disconnectRole(role){const socket=sockets[role];sockets[role]=null;if(socket)socket.close();});
 }
 function resumeProfiler(){
  if(!model.stopped)return;
  model.stopped=false;model.error='';model.selected='';model.report=null;model.more=false;model.page=0;
  refresh();render();
 }
 window.addEventListener('pagehide',suspendProfiler);
 window.addEventListener('pageshow',resumeProfiler);
 refresh();render();
})();
