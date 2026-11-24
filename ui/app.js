(() => {
'use strict';
const token = document.querySelector('meta[name="deskcare-token"]').content;
// Keep a separate lease per browser tab. Closing the last tab stops the Go
// server automatically; background tabs stay alive via their periodic ping.
const sessionID = (window.crypto?.randomUUID?.() || Array.from(crypto.getRandomValues(new Uint8Array(20)), b => b.toString(16).padStart(2, '0')).join('')).replace(/-/g, '');
let sessionIsOpen = false;
let exitRequested = false;
async function registerSession() {
  await api('/api/session/open','POST',{id:sessionID});
  sessionIsOpen = true;
}
function leaveSession() {
  if (!sessionIsOpen || exitRequested) return;
  sessionIsOpen = false;
  try {
    // Beacon survives most browser tab-close events, unlike a normal fetch.
    const blob = new Blob([JSON.stringify({id:sessionID,token})], {type:'text/plain;charset=UTF-8'});
    if (navigator.sendBeacon?.('/api/session/beacon',blob)) return;
  } catch (_) {}
  try {
    fetch('/api/session/close',{method:'POST',headers:{'X-DeskCare-Token':token,'Content-Type':'application/json'},body:JSON.stringify({id:sessionID}),keepalive:true}).catch(()=>{});
  } catch (_) {}
}
window.addEventListener('pagehide',leaveSession);

const $ = (id) => document.getElementById(id);
const state = {
  page:'dashboard', cleanView:'large', selected:new Set(), rootChoices:[], selectedRoots:new Set(),
  overview:null, fileOffset:0, cleanOffset:0, fileResults:[], cleanResults:[],
  fileTotal:0, cleanTotal:0, filesLoading:false, apps:[], dupData:null,
  driveIndex:0, automaticScanStarted:false, toastTimer:null, dialogResolve:null,
  showProtected:true, autoProtectionEnabled:true, trashBusy:false,
};
const categoryMeta = {
  photos:{icon:'▣', color:'#a88af6', bg:'#f2ecff'}, videos:{icon:'▶',color:'#e49b57',bg:'#fff2e7'},
  documents:{icon:'▤',color:'#6289e8',bg:'#edf2ff'}, audio:{icon:'♫',color:'#4abca4',bg:'#e6f8f3'},
  archives:{icon:'▥',color:'#c48e67',bg:'#f8efe8'},installers:{icon:'⬡',color:'#e47f91',bg:'#fff0f1'},
  others:{icon:'∙∙',color:'#8190a7',bg:'#eff2f6'}
};
const descriptions = {
  large:['용량 큰 파일','200MB 이상 파일을 확인하세요. 🔒 보호 항목 표시 여부는 상단 설정에서 선택합니다.'],
  old:['오래된 파일','선택한 모든 폴더의 90일 이상 수정되지 않은 파일입니다. 날짜만으로 삭제해도 되는지 판단할 수 없습니다.'],
  old_downloads:['오래된 다운로드','다운로드 폴더의 90일 이상 수정되지 않은 파일입니다. 현재 전체 파일과 같은 분석 결과를 사용합니다.'],
  installers:['설치·실행 파일','보호 모드가 켜져 있으면 삭제 불가입니다. 끄더라도 설치·실행 파일은 보호됩니다. 설치된 앱은 Windows 설정에서 제거하세요.'],
  duplicates:['중복 파일','현재 보호 모드에서 삭제 후보가 되는 파일만 SHA-256으로 비교합니다. 각 그룹에서 1개는 항상 보호합니다.']
};
function escapeHtml(x){return String(x??'').replace(/[&<>"']/g,s=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[s]));}
function formatSize(n){const units=['B','KB','MB','GB','TB','PB'];n=Number(n)||0;let i=0;while(Math.abs(n)>=1024&&i<units.length-1){n/=1024;i++;}return `${n>=100||i===0?n.toFixed(0):n>=10?n.toFixed(1):n.toFixed(2)} ${units[i]}`;}
function fmtDate(t){if(!t)return '—';const d=new Date(t*1000);return d.toLocaleDateString('ko-KR',{year:'numeric',month:'2-digit',day:'2-digit'});}
function debounce(fn,delay){let t;return (...args)=>{clearTimeout(t);t=setTimeout(()=>fn(...args),delay)}}
async function api(path,method='GET',data=undefined){
  const opts={method,headers:{'X-DeskCare-Token':token}};
  if(data!==undefined){opts.headers['Content-Type']='application/json';opts.body=JSON.stringify(data);}
  const res=await fetch(path,opts);
  let body;try{body=await res.json();}catch{throw new Error('연결이 끊어졌습니다. DeskCare를 다시 실행하세요.');}
  if(!res.ok)throw new Error(body.error||`요청 실패 (${res.status})`);
  return body;
}
function toast(msg,isError=false){const node=$('toast');node.textContent=msg;node.className=`toast show${isError?' error':''}`;clearTimeout(state.toastTimer);state.toastTimer=setTimeout(()=>node.className='toast',4400);}
function go(page){
  state.page=page;
  document.querySelectorAll('.page').forEach(e=>e.classList.toggle('active',e.id===`page-${page}`));
  document.querySelectorAll('.main-nav .nav-item').forEach(e=>e.classList.toggle('active',e.dataset.page===page));
  $('breadcrumbTitle').textContent=({dashboard:'대시보드',files:'전체 파일',cleanup:'스마트 정리',apps:'설치된 앱'})[page];
  if(page==='files')loadFiles();
  if(page==='cleanup')refreshCleanup();
  if(page==='apps')loadApps();
}
function renderDrives(){
  const drives=state.overview?.drives||[];
  $('driveSelect').innerHTML=drives.map((d,i)=>`<option value="${i}">${escapeHtml(d.name)}</option>`).join('')||'<option value="0">드라이브 없음</option>';
  state.driveIndex=Math.min(state.driveIndex,Math.max(0,drives.length-1));$('driveSelect').value=String(state.driveIndex);
  const d=drives[state.driveIndex];
  if(!d){$('diskPercent').textContent='—';$('diskUsed').textContent='—';$('diskFree').textContent='—';$('diskTotal').textContent='드라이브 정보를 불러올 수 없습니다';$('diskDonut').style.setProperty('--pct','0%');return;}
  const percent=d.total?Math.round(d.used/d.total*100):0;
  $('diskPercent').textContent=`${percent}%`; $('diskUsed').textContent=formatSize(d.used); $('diskFree').textContent=formatSize(d.free);
  $('diskTotal').textContent=`전체 ${formatSize(d.total)}`; $('diskDonut').style.setProperty('--pct',`${Math.min(100,Math.max(0,percent))}%`);
}
function renderCategories(){
  const overview=state.overview;if(!overview)return;
  const cats=[...overview.categories].sort((a,b)=>b.size-a.size);const max=Math.max(1,...cats.map(c=>c.size));
  $('categoryGrid').innerHTML=cats.map(c=>{const m=categoryMeta[c.key]||categoryMeta.others;return `<button class="category-card" data-category="${c.key}"><span class="icon-box" style="background:${m.bg};color:${m.color}">${m.icon}</span><strong>${escapeHtml(c.name)}</strong><b>${formatSize(c.size)}</b><small>${c.count.toLocaleString('ko-KR')}개 파일</small><div class="microbar"><span style="background:${m.color};width:${c.size/max*100}%"></span></div></button>`}).join('');
}
function renderRoots(){
  $('folderList').innerHTML=state.rootChoices.map((r,i)=>`<div class="folder-chip" title="${escapeHtml(r.path)}"><input type="checkbox" id="root-${i}" data-root="${i}" ${state.selectedRoots.has(r.path)?'checked':''}/><label for="root-${i}">📁 ${escapeHtml(r.name)}</label></div>`).join('')||'<span class="muted tiny">분석할 폴더를 추가하세요.</span>';
}
function renderSafetyControls(){
  $('showProtected').checked=state.showProtected;
  $('enableAutoProtection').checked=state.autoProtectionEnabled;
  $('protectionAlert').hidden=state.autoProtectionEnabled;
  $('safetyModeDetail').textContent=state.autoProtectionEnabled?'자동 보호 사용 중 · 시스템 필수 보호 유지':'추가 자동 보호 해제됨 · 시스템 필수 보호 유지';
  document.querySelector('.safety-settings').classList.toggle('relaxed',!state.autoProtectionEnabled);
}
async function updateProtectionMode(enabled){
  // Always require explicit consent to disable the file-extension allowlist.
  if(!enabled&&!confirm('추가 자동 보호를 끌까요?\n\n일반 사용자 폴더의 TXT·CSV·Markdown 문서만 추가 정리할 수 있습니다. 실행·설치·압축·설정 파일과 시스템·프로그램 경로는 계속 보호됩니다.\n\n파일 삭제 전 반드시 저장 위치를 확인하세요.')){
    renderSafetyControls();return;
  }
  $('enableAutoProtection').disabled=true;
  try {
    const result=await api('/api/protection','POST',{enabled});
    state.autoProtectionEnabled=result.autoProtectionEnabled;
    state.selected.clear(); state.dupData=null; state.fileOffset=0;state.cleanOffset=0;
    syncSelections(); await loadOverview();
    if(state.page==='files')await loadFiles();
    if(state.page==='cleanup')await refreshCleanup();
    toast(enabled?'자동 보호를 켰습니다.':'자동 보호를 껐습니다. 필수 시스템 보호는 유지됩니다.');
  }catch(e){toast(e.message,true);renderSafetyControls()}
  finally{$('enableAutoProtection').disabled=false;}
}
function renderOverview(){
  const o=state.overview;if(!o)return;
  renderDrives();renderCategories();
  const size=formatSize(o.totalSize).split(' ');
  $('indexedSize').innerHTML=`${escapeHtml(size[0])} <span>${escapeHtml(size[1]||'B')}</span>`;
  $('indexedCount').textContent=Number(o.fileCount||0).toLocaleString('ko-KR');
  $('indexPill').textContent=o.scan.state==='running'?'분석 중':o.scan.state==='done'?'분석 완료':o.scan.state==='cancelled'?'취소됨':'분석 준비';
  $('appCount').textContent=Number(o.appCount||0).toLocaleString('ko-KR');
  $('protectionStatus').textContent=`현재 보호 항목 ${Number(o.protectedFiles||0).toLocaleString('ko-KR')}개 (${formatSize(o.protectedSize||0)}). 시스템·프로그램 폴더는 분석에서 제외됩니다.`;
  state.autoProtectionEnabled=o.autoProtectionEnabled!==false;
  renderSafetyControls();
  const isBusy=o.scan.state==='running'||o.duplicates.state==='running';
  $('scanAgain').disabled=isBusy;$('startScan').disabled=isBusy;
  const w=$('scanStatus');w.hidden=!isBusy;
  if(isBusy){const task=o.scan.state==='running'?o.scan:o.duplicates;
    $('workStatusTitle').textContent=o.scan.state==='running'?'파일 분석 중':'중복 파일 검사 중';
    $('workStatusDetail').textContent=`${task.processed.toLocaleString('ko-KR')}개 확인 · ${task.current||'파일을 검사하고 있습니다'}`;
  }
  if(o.scan.limited){$('indexPill').textContent='일부 분석';$('indexPill').title='파일 수가 35만 개를 넘어 일부 파일만 분류했습니다';}
}
async function loadOverview(){
  const previous=state.overview?.scan.state;
  const oldDup=state.overview?.duplicates.state;
  const o=await api('/api/overview');state.overview=o;
  if(!state.rootChoices.length){state.rootChoices=[...(o.roots||[])];state.selectedRoots=new Set(o.selectedRoots||o.roots.map(x=>x.path));renderRoots();}
  renderOverview();
  const finished=(previous==='running'&&o.scan.state!=='running')||(oldDup==='running'&&o.duplicates.state!=='running');
  if(finished){ if(o.scan.state==='done')toast(`${o.fileCount.toLocaleString('ko-KR')}개 파일 분석을 마쳤습니다.`);
    if(o.duplicates.state==='done')toast('중복 파일 검사가 완료되었습니다.');
    if(state.page==='files')loadFiles();
    if(state.page==='cleanup')refreshCleanup();
  }
  if(!state.automaticScanStarted&&o.scan.state==='idle'&&state.rootChoices.length){
    state.automaticScanStarted=true;try{await startScan(true);}catch(e){toast(e.message,true)}
  }
}
async function startScan(silent=false){
  const paths=[...state.selectedRoots];if(!paths.length){if(!silent)toast('분석할 폴더를 먼저 선택하세요.',true);return;}
  await api('/api/scan','POST',{paths});state.selected.clear();syncSelections();
  if(!silent)toast('선택한 폴더의 파일 분석을 시작했습니다.');await loadOverview();
}
function fileIcon(category){return categoryMeta[category]?.icon||'□';}
function fileRow(f){const safe=!!f.canTrash;const reason=escapeHtml(f.safetyReason||'');return `<tr class="${safe?'':'protected-row'}"><td class="check-col"><input class="file-check" type="checkbox" data-id="${escapeHtml(f.id)}" aria-label="${escapeHtml(f.name)} 선택" ${safe?'':'disabled title="보호된 파일"'} ${safe&&state.selected.has(f.id)?'checked':''}></td><td><div class="file-label"><span class="file-emoji">${fileIcon(f.category)}</span><div class="file-meta"><strong title="${escapeHtml(f.name)}">${escapeHtml(f.name)}</strong>${safe?'':' <span class="protect-chip" title="'+reason+'">🔒 자동 보호</span>'}<small title="${escapeHtml(f.path)}">${escapeHtml(f.path)}</small>${safe?'':`<small class="protection-reason">${reason}</small>`}</div></div></td><td>${fmtDate(f.modified)}</td><td class="right"><strong>${formatSize(f.size)}</strong></td><td class="action-col"><button class="icon-button file-open" data-id="${escapeHtml(f.id)}" title="파일 위치 열기">↗</button></td></tr>`;}
function emptyMessage(target,msg){$(target).innerHTML=`<tr><td class="empty-row" colspan="5">${escapeHtml(msg)}</td></tr>`;}
async function loadFiles(){
  try{
    const params=new URLSearchParams({category:$('fileCategory').value,view:$('fileSmartFilter').value,q:$('fileSearch').value,sort:$('fileSort').value,offset:state.fileOffset,limit:100,showProtected:String(state.showProtected)});
    const result=await api('/api/files?'+params);
    state.fileResults=result.files;state.fileTotal=result.total;
    $('fileListSummary').textContent=`${result.total.toLocaleString('ko-KR')}개 파일 · ${formatSize(result.totalSize)} · ${$('fileSmartFilter').selectedOptions[0].text} · ${state.showProtected?'🔒 보호 항목 표시 중':'🔒 보호 항목 숨김'}`;
    if(result.files.length)$('fileTableBody').innerHTML=result.files.map(fileRow).join('');else emptyMessage('fileTableBody',$('fileSmartFilter').value==='duplicates'&&state.overview?.duplicates.state!=='done'?'스마트 정리에서 중복 검사를 먼저 실행하세요.':'표시할 파일이 없습니다. 분석 위치 또는 필터를 확인하세요.');
    $('filesPageInfo').textContent=result.total?`${state.fileOffset+1}–${Math.min(state.fileOffset+100,result.total)} / ${result.total.toLocaleString('ko-KR')}`:'0개 파일';
    $('filesPrev').disabled=state.fileOffset===0;$('filesNext').disabled=state.fileOffset+100>=result.total;
    $('selectPage').checked=result.files.length>0&&result.files.every(f=>state.selected.has(f.id));syncSelections();
  }catch(e){toast(e.message,true)}
}
function cleanupParams(){return new URLSearchParams({view:state.cleanView,offset:state.cleanOffset,limit:100,sort:'size_desc',showProtected:String(state.showProtected)})}
async function refreshCleanup(){
  const [title,desc]=descriptions[state.cleanView];$('cleanTitle').textContent=title;$('cleanDescription').textContent=desc;
  document.querySelectorAll('.clean-tab').forEach(e=>e.classList.toggle('active',e.dataset.view===state.cleanView));
  const dup=state.cleanView==='duplicates';$('duplicatePanel').hidden=!dup;$('cleanListPanel').hidden=dup;
  if(dup){await loadDuplicates();return}
  try{
    const result=await api('/api/files?'+cleanupParams());
    state.cleanResults=result.files;state.cleanTotal=result.total;
    $('cleanResultCount').textContent=`${result.total.toLocaleString('ko-KR')}개 파일`;
    $('cleanListSummary').textContent=`${result.total.toLocaleString('ko-KR')}개 파일 · ${formatSize(result.totalSize)} · ${state.showProtected?'보호 항목 표시 중':'보호 항목 숨김'}`;
    if(result.files.length)$('cleanTableBody').innerHTML=result.files.map(fileRow).join('');else emptyMessage('cleanTableBody','해당하는 파일이 없습니다.');
    $('cleanPageInfo').textContent=result.total?`${state.cleanOffset+1}–${Math.min(state.cleanOffset+100,result.total)} / ${result.total.toLocaleString('ko-KR')}`:'0개 파일';
    $('cleanPrev').disabled=state.cleanOffset===0;$('cleanNext').disabled=state.cleanOffset+100>=result.total;
    syncSelections();
  }catch(e){toast(e.message,true)}
}
async function loadDuplicates(){
  try{
    const data=await api('/api/duplicates');state.dupData=data;
    const {state:job,groups,reclaimable}=data;
    $('cleanResultCount').textContent=`${groups.length}개 그룹`;
    $('dupProgress').hidden=job.state!=='running';
    if(job.state==='running')$('dupProgress').textContent=`${job.processed.toLocaleString('ko-KR')}개 파일 내용을 비교하고 있습니다… ${job.current}`;
    $('findDuplicates').disabled=job.state==='running'||state.overview?.scan.state==='running';
    $('findDuplicates').textContent=job.state==='running'?'검사 진행 중':job.state==='done'?'중복 다시 검사':'중복 검사 시작';
    $('selectDuplicateExtra').hidden=!groups.length;
    $('dupSummary').textContent=job.state==='done'?`${groups.length}개 중복 그룹 · 최대 ${formatSize(reclaimable)} 정리 가능 (각 그룹에서 원본 1개 보관 기준)`:
      job.state==='running'?'디스크 속도와 후보 파일 크기에 따라 검사 시간이 걸릴 수 있습니다.':
      '중복 검사는 현재 삭제 가능한 파일만 비교합니다. 각 그룹의 원본 1개는 항상 보호합니다.';
    $('duplicateGroups').innerHTML=groups.slice(0,120).map((g,i)=>`<div class="panel dup-group"><div class="dup-group-head"><strong>중복 그룹 ${i+1} · ${formatSize(g.size)} 파일</strong><small>${g.files.length}개 발견 · 절약 가능 ${formatSize(g.extraBytes)}</small></div>${g.files.filter(f=>state.showProtected||f.canTrash).map((f,j)=>`<label class="dup-file"><input type="checkbox" class="file-check" data-id="${escapeHtml(f.id)}" ${f.canTrash?'':'disabled'} ${f.canTrash&&state.selected.has(f.id)?'checked':''}><span><strong>${escapeHtml(f.name)} ${!f.canTrash?'· 🔒 보관용 보호':''}</strong><small>${escapeHtml(f.path)}</small></span><button class="icon-button file-open" data-id="${escapeHtml(f.id)}" type="button" title="파일 위치 열기">↗</button></label>`).join('')}</div>`).join('');
    if(groups.length>120)toast('화면에는 용량 절약 효과가 큰 중복 그룹 120개만 표시합니다.');
    syncSelections();
  }catch(e){toast(e.message,true)}
}
function syncSelections(){
  const n=state.selected.size;
  $('selectionCount').textContent=`선택 ${n.toLocaleString('ko-KR')}개`; $('cleanSelectionCount').textContent=`선택 ${n.toLocaleString('ko-KR')}개`;
  $('trashFiles').disabled=n===0||state.trashBusy;$('trashCleanup').disabled=n===0||state.trashBusy;
  $('selectPage').checked=state.fileResults.some(f=>f.canTrash)&&state.fileResults.filter(f=>f.canTrash).every(f=>state.selected.has(f.id));
  document.querySelectorAll('.file-check').forEach(cb=>cb.checked=!cb.disabled&&state.selected.has(cb.dataset.id));
}
function selectPage(files){for(const f of files)if(f.canTrash)state.selected.add(f.id);syncSelections();}
async function reveal(id){try{await api('/api/open','POST',{id});}catch(e){toast(e.message,true)}}
function askConfirm(count){return new Promise(resolve=>{
  state.dialogResolve=resolve;$('dialogDescription').textContent=`${count.toLocaleString('ko-KR')}개 파일을 Windows 휴지통으로 옮깁니다. ${state.autoProtectionEnabled?'':'⚠ 자동 보호 해제 상태: 일반 텍스트 문서도 포함될 수 있습니다. '}프로그램에 필요한 파일인지 완벽히 확인할 수 없습니다. 복원하려면 Windows 휴지통을 이용하세요.`;
  $('confirmBackdrop').hidden=false;
})}
function closeDialog(accepted){$('confirmBackdrop').hidden=true;if(state.dialogResolve){const cb=state.dialogResolve;state.dialogResolve=null;cb(accepted)}}
async function trashSelected(){
  if(state.trashBusy)return; // avoid concurrent clicks and double requests
  const ids=[...state.selected];if(!ids.length)return;
  if(!await askConfirm(ids.length))return;
  let removed=0;const failures=[];
  let requestError='';
  state.trashBusy=true;
  syncSelections();
  try{
    for(let i=0;i<ids.length;i+=50){
      const response=await api('/api/trash','POST',{ids:ids.slice(i,i+50)});
      // Backward-compatible with old versions that returned null for empty lists.
      // Unexpected responses are treated as errors, not as successful deletes.
      if(!response || (response.removed!=null&&!Array.isArray(response.removed)) ||
         (response.failed!=null&&!Array.isArray(response.failed))){
        throw new Error('휴지통 처리 결과를 확인할 수 없습니다. Windows 휴지통을 먼저 확인하세요.');
      }
      const moved=Array.isArray(response.removed)?response.removed:[];
      const rejected=Array.isArray(response.failed)?response.failed:[];
      for(const id of moved){state.selected.delete(id);removed++;}
      failures.push(...rejected);
      if(!moved.length&&!rejected.length){
        throw new Error('서버가 처리 결과를 반환하지 않았습니다. Windows 휴지통을 확인하세요.');
      }
    }
  }catch(e){
    requestError=e.message||'휴지통 처리 중 오류가 발생했습니다.';
  }finally{
    state.trashBusy=false;
    // The server may have already moved some files before a response error.
    // Always refresh the live file inventory rather than relying on the toast.
    try{
      await loadOverview();
      if(state.page==='files')await loadFiles();
      if(state.page==='cleanup')await refreshCleanup();
    }catch(e){
      requestError += (requestError?' / ':'')+'목록 갱신 실패: '+e.message;
    }
    syncSelections();
  }
  if(requestError){
    toast(`확인 필요: ${removed}개 처리 확인. ${requestError} 중복 실행 전에 휴지통을 확인하세요.`,true);
  }else if(failures.length){
    toast(`${removed}개 파일을 휴지통으로 이동했습니다. ${failures.length}개 실패: ${failures[0]}`,true);
  }else{
    toast(`${removed}개 파일을 Windows 휴지통으로 이동했습니다.`);
  }
}

async function loadApps(){
  try{
    const qs=new URLSearchParams({q:$('appsSearch').value,sort:$('appsSort').value});
    const data=await api('/api/apps?'+qs);
    state.apps=data.apps;
    $('appCount').textContent=Number(state.overview?.appCount||data.total).toLocaleString('ko-KR');
    $('appSize').textContent=formatSize(data.estimatedTotal);
    $('appsTableBody').innerHTML=data.apps.length?data.apps.map(a=>{const path=a.pathKnown?escapeHtml(a.installPath):'경로 미제공';const open=a.pathExists?`<button class="btn btn-light btn-small app-folder-open" data-app-id="${escapeHtml(a.id)}" type="button" title="Windows 탐색기에서 설치 폴더 열기">폴더 열기 ↗</button>`:'';const status=a.pathKnown&&!a.pathExists?'<small class="path-warning">경로 없음 · 이동되었거나 삭제됨</small>':'';return `<tr><td><span class="app-ico">▦</span><strong>${escapeHtml(a.name)}</strong></td><td>${escapeHtml(a.publisher||'알 수 없음')}</td><td class="app-path-cell"><span title="${path}">${path}</span>${status}${open}</td><td class="right"><strong>${a.sizeKnown?formatSize(a.size):'미제공'}</strong></td></tr>`}).join(''):`<tr><td colspan="4" class="empty-row">일치하는 앱이 없습니다.</td></tr>`;
  }catch(e){toast(e.message,true)}
}
async function exportFiles(){
  try{
    const response=await fetch('/api/export',{headers:{'X-DeskCare-Token':token}});
    if(!response.ok)throw new Error('CSV 저장에 실패했습니다.');
    const blob=await response.blob();const url=URL.createObjectURL(blob);
    const a=document.createElement('a');a.href=url;a.download='DeskCare_파일목록.csv';document.body.appendChild(a);a.click();a.remove();
    setTimeout(()=>URL.revokeObjectURL(url),2000);
  }catch(e){toast(e.message,true)}
}
function bindUI(){
  $('showProtected').addEventListener('change',e=>{
    state.showProtected=e.target.checked;state.fileOffset=0;state.cleanOffset=0;
    state.selected.clear();syncSelections();renderSafetyControls();
    if(state.page==='files')loadFiles();
    if(state.page==='cleanup')refreshCleanup();
  });
  $('enableAutoProtection').addEventListener('change',e=>updateProtectionMode(e.target.checked));
  document.querySelectorAll('.main-nav [data-page]').forEach(b=>b.addEventListener('click',()=>go(b.dataset.page)));
  $('goAllFiles').addEventListener('click',()=>go('files'));
  $('scanAgain').addEventListener('click',()=>startScan().catch(e=>toast(e.message,true)));
  $('startScan').addEventListener('click',()=>startScan().catch(e=>toast(e.message,true)));
  $('cancelJob').addEventListener('click',async()=>{try{await api('/api/cancel','POST',{});toast('취소 요청을 보냈습니다.');}catch(e){toast(e.message,true)}});
  $('driveSelect').addEventListener('change',e=>{state.driveIndex=Number(e.target.value);renderDrives()});
  $('categoryGrid').addEventListener('click',e=>{const card=e.target.closest('[data-category]');if(!card)return;$('fileCategory').value=card.dataset.category;$('fileSmartFilter').value='all';state.fileOffset=0;go('files')});
  $('folderList').addEventListener('change',e=>{if(!e.target.matches('[data-root]'))return;const root=state.rootChoices[Number(e.target.dataset.root)];if(!root)return;if(e.target.checked)state.selectedRoots.add(root.path);else state.selectedRoots.delete(root.path)});
  $('addFolder').addEventListener('click',async()=>{try{
    const root=await api('/api/pick-folder','POST',{});
    if(!state.rootChoices.some(r=>r.path.toLowerCase()===root.path.toLowerCase()))state.rootChoices.push(root);
    state.selectedRoots.add(root.path);renderRoots();toast('폴더를 추가했습니다. 분석 버튼을 누르세요.');
  }catch(e){if(!e.message.includes('취소'))toast(e.message,true)}});
  document.querySelectorAll('[data-clean-view]').forEach(b=>b.addEventListener('click',()=>{state.cleanView=b.dataset.cleanView;state.cleanOffset=0;go('cleanup')}));
  $('viewInAllFiles').addEventListener('click',()=>{
    $('fileSmartFilter').value=state.cleanView;
    $('fileCategory').value='all';$('fileSearch').value='';state.fileOffset=0;
    state.selected.clear();syncSelections();
    go('files');
  });
  $('fileSmartFilter').addEventListener('change',()=>{state.fileOffset=0;state.selected.clear();syncSelections();loadFiles()});
  $('fileSearch').addEventListener('input',debounce(()=>{state.fileOffset=0;loadFiles()},300));
  $('fileCategory').addEventListener('change',()=>{state.fileOffset=0;loadFiles()});
  $('fileSort').addEventListener('change',()=>{state.fileOffset=0;loadFiles()});
  $('filesPrev').addEventListener('click',()=>{state.fileOffset=Math.max(0,state.fileOffset-100);loadFiles()});
  $('filesNext').addEventListener('click',()=>{state.fileOffset+=100;loadFiles()});
  $('selectPage').addEventListener('change',e=>{for(const f of state.fileResults){if(e.target.checked&&f.canTrash)state.selected.add(f.id);else state.selected.delete(f.id)}syncSelections()});
  $('clearSelection').addEventListener('click',()=>{state.selected.clear();syncSelections()});
  $('trashFiles').addEventListener('click',trashSelected);$('trashCleanup').addEventListener('click',trashSelected);
  document.querySelectorAll('.clean-tab').forEach(b=>b.addEventListener('click',()=>{state.cleanView=b.dataset.view;state.cleanOffset=0;refreshCleanup()}));
  $('cleanPrev').addEventListener('click',()=>{state.cleanOffset=Math.max(0,state.cleanOffset-100);refreshCleanup()});
  $('cleanNext').addEventListener('click',()=>{state.cleanOffset+=100;refreshCleanup()});
  $('cleanSelectPage').addEventListener('click',()=>selectPage(state.cleanResults));
  $('cleanClear').addEventListener('click',()=>{state.selected.clear();syncSelections()});
  $('findDuplicates').addEventListener('click',async()=>{try{await api('/api/duplicates/start','POST',{});await loadOverview();await loadDuplicates();}catch(e){toast(e.message,true)}});
  $('selectDuplicateExtra').addEventListener('click',()=>{for(const g of state.dupData?.groups||[]){for(const f of g.files)if(f.canTrash)state.selected.add(f.id)}syncSelections();toast('각 그룹의 보호용 사본은 제외하고 선택했습니다. 삭제 전 파일 위치를 확인하세요.');});
  $('appsSearch').addEventListener('input',debounce(loadApps,250));$('appsSort').addEventListener('change',loadApps);
  $('openAppSettings').addEventListener('click',async()=>{try{await api('/api/apps/settings','POST',{})}catch(e){toast(e.message,true)}});
  $('appsTableBody').addEventListener('click',async e=>{const btn=e.target.closest('.app-folder-open');if(!btn)return;btn.disabled=true;try{await api('/api/apps/open','POST',{id:btn.dataset.appId})}catch(err){toast(err.message,true)}finally{btn.disabled=false}});
  $('exportCSV').addEventListener('click',exportFiles);
  $('fileTableBody').addEventListener('change',handleSelection);$('cleanTableBody').addEventListener('change',handleSelection);$('duplicateGroups').addEventListener('change',handleSelection);
  for(const id of ['fileTableBody','cleanTableBody','duplicateGroups'])$(id).addEventListener('click',e=>{const b=e.target.closest('.file-open');if(b){e.preventDefault();reveal(b.dataset.id)}});
  $('dialogCancel').addEventListener('click',()=>closeDialog(false));$('dialogConfirm').addEventListener('click',()=>closeDialog(true));
  $('confirmBackdrop').addEventListener('click',e=>{if(e.target===$('confirmBackdrop'))closeDialog(false)});
  document.addEventListener('keydown',e=>{if(e.key==='Escape'&&!$('confirmBackdrop').hidden)closeDialog(false)});
  $('exitTop').addEventListener('click',()=>$('exitApp').click());
  $('exitApp').addEventListener('click',async()=>{
    if(!confirm('DeskCare를 종료할까요? 파일 분석도 함께 중단됩니다.'))return;
    try{await api('/api/exit','POST',{});exitRequested=true;sessionIsOpen=false;document.body.innerHTML='<div style="font-family:Segoe UI,Malgun Gothic,sans-serif;text-align:center;margin:20vh auto;color:#43516b"><h1>DeskCare가 종료되었습니다.</h1><p>이 탭을 닫아도 됩니다.</p></div>';}catch(e){toast(e.message,true)}
  });
}
function handleSelection(e){if(!e.target.matches('.file-check')||e.target.disabled)return;if(e.target.checked)state.selected.add(e.target.dataset.id);else state.selected.delete(e.target.dataset.id);syncSelections()}
async function init(){bindUI();try{await registerSession();await loadOverview();}catch(e){toast('앱에 연결하지 못했습니다: '+e.message,true)}
  setInterval(()=>{if(sessionIsOpen&&!exitRequested)api('/api/session/ping','POST',{id:sessionID}).catch(e=>console.warn('세션 유지 실패:',e.message));},2500);
  setInterval(async()=>{
    if(!state.overview)return;
    if(state.overview.scan.state==='running'||state.overview.duplicates.state==='running'){
      try{await loadOverview();if(state.page==='cleanup'&&state.cleanView==='duplicates'&&state.overview.duplicates.state==='running')await loadDuplicates();}catch(e){console.warn(e)}
    }
  },1050);
}
document.addEventListener('DOMContentLoaded',init);
})();
