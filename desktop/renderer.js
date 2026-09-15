const api=window.deskbridge;
const $=id=>document.getElementById(id);
let state,dirty=false,direction,peer,toastTimer;
function icons(){lucide.createIcons();}
function toast(message){$('toast').textContent=message;$('toast').hidden=false;clearTimeout(toastTimer);toastTimer=setTimeout(()=>$('toast').hidden=true,7000);}
async function action(promise){const result=await promise;if(result?.error)toast(result.error);else if(result?.message)toast(result.message);return result;}
function selectPosition(next){direction=next;dirty=true;$('apply').disabled=state?.side!=='a';renderBoard();}
function renderBoard(){
 const positions={left:'2 / 1',right:'2 / 3',up:'1 / 2',down:'3 / 2'};
 $('peer-device').style.gridArea=positions[direction]||positions.right;
 $('peer-position').textContent={left:'Sol tarafta',right:'Sag tarafta',up:'Ust tarafta',down:'Alt tarafta'}[direction];
 document.querySelectorAll('[data-move]').forEach(b=>b.classList.toggle('selected',b.dataset.move===direction));
}
function render(next){state=next;if(!dirty){direction=state.layout.direction;peer=state.layout.peer;}
 $('connection').classList.toggle('online',state.connected);$('connection-label').textContent=state.connected?'Cihazlar bagli':'Baglanti bekleniyor';
 $('footer-status').textContent=state.connected?'Sifreli aktarim etkin':'Diger cihaz bekleniyor';
 $('local-name').textContent=state.layout.local;$('peer-name').textContent=peer;$('peer-status').textContent=state.connected?'BAGLI':'CEVRIMDISI';
 $('capability').textContent=state.clipboardSupported?'Hazir':state.connected?'Guncelleme gerekli':'Bekleniyor';
 const names=[...new Set([...state.layout.screens.filter(n=>n!==state.layout.local),peer])];
 if(JSON.stringify([...$('peer-select').options].map(o=>o.value))!==JSON.stringify(names))$('peer-select').replaceChildren(...names.map(n=>{const o=document.createElement('option');o.value=n;o.textContent=n;return o;}));
 $('peer-select').value=peer;
 $('clipboard-toggle').checked=state.preferences.clipboard;$('autostart-toggle').checked=state.preferences.autoStart;
 $('clipboard-note').textContent=state.clipboardSupported?'Kopyalanan dosyalar eslestirilmis cihaza gonderilir.':'Karsi cihazda DeskBridge Desktop 0.3.0 gerekli.';
 $('choose-files').disabled=!state.connected;$('transfer-add').disabled=!state.connected;
 $('file-state').textContent=state.connected?'Bagli cihaza gonder':'Cihaz baglantisi bekleniyor';
 $('count').textContent=state.transfers.length;$('last-transfer').textContent=state.transfers[0]?.name||'Henuz aktarim yok';
 const list=$('transfer-list');list.replaceChildren();
 if(!state.transfers.length){const e=document.createElement('div');e.className='empty';e.textContent='Henuz aktarim yok';list.append(e);}
 for(const item of state.transfers){
  const row=document.createElement('div');row.className='transfer-row';row.draggable=item.status==='done';row.addEventListener('dragstart',e=>{e.preventDefault();api.startDrag(item.id);});
  const icon=document.createElement('i');icon.dataset.lucide=item.direction==='received'?'file-down':'file-up';row.append(icon);
  const info=document.createElement('div');info.className='transfer-info';const name=document.createElement('strong');name.textContent=item.name;const meta=document.createElement('small');meta.textContent=(item.direction==='received'?'Alindi':'Gonderildi')+' · '+new Date(item.time).toLocaleTimeString('tr-TR',{hour:'2-digit',minute:'2-digit'});info.append(name,meta);row.append(info);
  const status=document.createElement('span');status.className='transfer-status'+(item.status==='error'?' failed':'');status.textContent=item.status==='sending'?item.progress+'%':item.status==='error'?'Basarisiz':'Tamamlandi';status.title=item.error||'';row.append(status);
  for(const [symbol,title,handler] of [['copy','Dosyayi kopyala',()=>action(api.copyFiles(item.id))],['folder-open','Klasorde goster',()=>action(api.showFile(item.id))]]){const b=document.createElement('button');b.className='icon-button';b.title=title;b.setAttribute('aria-label',title);const i=document.createElement('i');i.dataset.lucide=symbol;b.append(i);b.onclick=handler;b.disabled=item.status!=='done';row.append(b);}list.append(row);
 }
 if(!state.paired&&!$('pair-dialog').open){api.generatePairing().then(result=>{$('pair-code').value=result.code;$('pair-dialog').showModal();});}
 renderBoard();icons();
}
document.querySelectorAll('.tab').forEach(tab=>tab.onclick=()=>{document.querySelectorAll('.tab').forEach(t=>t.classList.toggle('active',t===tab));document.querySelectorAll('.view').forEach(v=>v.classList.toggle('active',v.id===tab.dataset.view));});
document.querySelectorAll('[data-move]').forEach(b=>b.onclick=()=>selectPosition(b.dataset.move));
document.querySelectorAll('.slot').forEach(slot=>{slot.onclick=()=>selectPosition(slot.dataset.direction);slot.ondragover=e=>{if(e.dataTransfer.types.includes('application/x-deskbridge-device')){e.preventDefault();slot.classList.add('drag-over');}};slot.ondragleave=()=>slot.classList.remove('drag-over');slot.ondrop=e=>{e.preventDefault();slot.classList.remove('drag-over');if(e.dataTransfer.getData('application/x-deskbridge-device'))selectPosition(slot.dataset.direction);};});
$('peer-device').ondragstart=e=>{e.dataTransfer.setData('application/x-deskbridge-device','peer');e.dataTransfer.effectAllowed='move';};
$('peer-select').onchange=e=>{peer=e.target.value;selectPosition(direction);$('peer-name').textContent=peer;};
$('apply').onclick=async()=>{const result=await action(api.applyLayout({peer,direction}));if(!result?.error){dirty=false;$('apply').disabled=true;}};
$('choose-files').onclick=$('transfer-add').onclick=()=>action(api.chooseFiles());
$('open-folder').onclick=()=>action(api.folder());$('reconnect').onclick=()=>action(api.connect());
$('clipboard-toggle').onchange=e=>action(api.configure({clipboard:e.target.checked}));$('autostart-toggle').onchange=e=>action(api.configure({autoStart:e.target.checked}));
$('generate-code').onclick=async()=>{$('pair-code').value=(await api.generatePairing()).code;};
$('relay-setup').onclick=()=>action(api.openRelaySetup());
$('copy-code').onclick=()=>action(api.copyText($('pair-code').value)).then(result=>{if(!result?.error)toast('Eslesme kodu kopyalandi');});
$('pair-form').onsubmit=async e=>{e.preventDefault();const result=await action(api.pair({url:$('relay-url').value,code:$('pair-code').value,side:$('pair-side').value}));if(!result?.error){$('pair-dialog').close();$('pair-code').value='';}};
document.addEventListener('dragover',e=>{if(e.dataTransfer.types.includes('Files')){e.preventDefault();$('dropzone').classList.add('drag-over');}});
document.addEventListener('drop',e=>{if(e.dataTransfer.files.length){e.preventDefault();$('dropzone').classList.remove('drag-over');action(api.sendDrop([...e.dataTransfer.files]));}});
document.addEventListener('dragleave',e=>{if(!e.relatedTarget)$('dropzone').classList.remove('drag-over');});
api.changed(render);api.state().then(render);icons();
