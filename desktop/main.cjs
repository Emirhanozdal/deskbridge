const { app, BrowserWindow, ipcMain, dialog, Menu, Tray, nativeImage, shell, clipboard, ClipboardItem } = require('electron');
const fs = require('node:fs');
const fsp = fs.promises;
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const net = require('node:net');
const { spawn, execFileSync } = require('node:child_process');
const { randomUUID, randomBytes } = require('node:crypto');
const { readLayout, changeLayout } = require('./layout.cjs');
const { readFiles, fileItem } = require('./clipboard.cjs');
const { resolveEngine } = require('./engine.cjs');
const { inputSettings } = require('./input-settings.cjs');
const { createRemote } = require('./remote.cjs');

app.setName('DeskBridge');
if (!app.requestSingleInstanceLock()) app.quit();
let win, tray, relayProcess, coreProcess, coreRestartTimer, closing = false, pollBusy = false, clipboardBusy = false;
let remote = null;
let connected = false, clipboardSupported = false, lastClipboard = '', error = '', transfers = [];
let latency = null, lastSeen = 0;
let preferences = { clipboard: false, direction: 'left', autoStart: false };
const configDir = process.platform === 'darwin' ? path.join(os.homedir(),'Library','Application Support','deskbridge') : path.join(process.env.XDG_CONFIG_HOME || path.join(os.homedir(),'.config'),'deskbridge');
const relayFile = path.join(configDir,'relay.json');
const prefsFile = path.join(configDir,'desktop.json');
const inbox = path.join(configDir,'clipboard-inbox');
const historyFile = path.join(configDir,'history.json');
const defaultRelay = 'https://deskbridge-relay.emirhanozdall.workers.dev';
const deployRelay = 'https://deploy.workers.cloudflare.com/?url=https://github.com/Emirhanozdal/deskbridge/tree/main/relay';
const deskflowDir = process.platform === 'darwin' ? path.join(os.homedir(),'Library','Deskflow') : path.join(process.env.XDG_CONFIG_HOME || path.join(os.homedir(),'.config'),'Deskflow');
const settingsFile = path.join(deskflowDir,'Deskflow.conf');
const layoutFile = path.join(deskflowDir,'deskflow-server.conf');
let layout = {local:os.hostname(),peer:'linux-pc',direction:'right',screens:[]};
function readJSON(file, fallback) { try { return JSON.parse(fs.readFileSync(file,'utf8')); } catch { return fallback; } }
function saveJSON(file, data) { fs.mkdirSync(path.dirname(file),{recursive:true,mode:0o700}); const temp=file+'.tmp'; fs.writeFileSync(temp,JSON.stringify(data,null,2),{mode:0o600}); fs.renameSync(temp,file); }
function relayConfig() { return readJSON(relayFile, {}); }
function binary() {
  // relay CLI is deskbridge.exe on Windows, deskbridge elsewhere
  const names=process.platform==='win32'?['deskbridge.exe','deskbridge']:['deskbridge'];
  const dirs=[process.resourcesPath, path.join(__dirname,'..','build')];
  for(const dir of dirs)for(const name of names){const p=path.join(dir,name);if(fs.existsSync(p))return p;}
  return names[0];
}
function snapshot() { const cfg=relayConfig(); return { connected, clipboardSupported, error, layout, preferences, transfers:transfers.slice(0,60), paired:!!cfg.code, side:cfg.side, managed:!!relayProcess, latency, lastSeen, engineRunning:!!coreProcess, version:'0.3.7' }; }
function emit() { if(win && !win.isDestroyed()) win.webContents.send('changed',snapshot()); }
function record(item) { transfers.unshift({id:randomUUID(),time:Date.now(),...item}); transfers=transfers.slice(0,200); saveJSON(historyFile,transfers); emit(); return transfers[0]; }
function request(endpoint, method='GET', body) {
  return new Promise((resolve,reject)=>{
    const cfg=relayConfig();
    const req=http.request({hostname:'127.0.0.1',port:47890,path:endpoint,method,headers:{'X-DeskBridge-Token':cfg.code || '', 'Content-Type':'application/json'},timeout:4000},res=>{
      let text=''; res.setEncoding('utf8'); res.on('data',c=>{text+=c;if(text.length>65536)req.destroy(Error('Invalid response'));}); res.on('end',()=>resolve({status:res.statusCode,text}));
    }); req.on('error',reject); req.on('timeout',()=>req.destroy(Error('Baglanti zaman asimina ugradi'))); if(body) req.write(JSON.stringify(body)); req.end();
  });
}
async function sendFile(file, item) {
  const stat=await fsp.stat(file); if(!stat.isFile())throw Error('Su an yalnizca dosyalar gonderilebilir');
  if(stat.size>5*1024**3)throw Error('Dosya 5 GB sinirini asiyor');
  return new Promise((resolve,reject)=>{
    const req=http.request({hostname:'127.0.0.1',port:47890,path:'/upload?name='+encodeURIComponent(path.basename(file)),method:'POST',headers:{'X-DeskBridge-Token':relayConfig().code,'Content-Type':'application/octet-stream','Content-Length':stat.size}},res=>{
      let text='';res.setEncoding('utf8');res.on('data',c=>{text+=c;});res.on('end',()=>res.statusCode===200 && text.startsWith('saved ')?resolve(text.slice(6).trim()):reject(Error(text || 'Aktarim basarisiz')));
    });
    const stream=fs.createReadStream(file); let sent=0,last=0;
    stream.on('data',b=>{sent+=b.length;item.progress=stat.size?Math.round(sent/stat.size*100):100;if(Date.now()-last>200){last=Date.now();emit();}});
    req.on('error',e=>{stream.destroy();reject(e);});req.setTimeout(30000,()=>req.destroy(Error('Aktarim durdu')));stream.on('error',e=>req.destroy(e));stream.pipe(req);
  });
}
async function sendFiles(files, asClipboard=false) {
  if(!connected)throw Error('Diger cihaz bagli degil');
  if(!Array.isArray(files)||!files.length||files.length>100)throw Error('Gecersiz dosya secimi');
  if(asClipboard&&!clipboardSupported)throw Error('Dosya panosu icin diger cihazda DeskBridge 0.3.0 veya ustu gerekli');
  const saved=[];
  for(const file of files){
    if(typeof file!=='string'||!path.isAbsolute(file))throw Error('Gecersiz dosya yolu');
    const item=record({name:path.basename(file),paths:[file],direction:'sent',status:'sending',progress:0});
    try {saved.push(await sendFile(file,item));item.status='done';item.progress=100;}catch(e){item.status='error';item.error=e.message;emit();throw e;}
    saveJSON(historyFile,transfers);emit();
  }
  if(asClipboard){const res=await request('/clipboard','POST',{files:saved});if(res.status!==202)throw Error(res.text);}
  return {ok:true};
}
async function copyLocalFiles(files) {
  lastClipboard=JSON.stringify(files);
  await clipboard.write([fileItem(files,ClipboardItem)]);
}
async function pollClipboard() {
  if(clipboardBusy)return;clipboardBusy=true;
  try {
    const events=await fsp.readdir(inbox).catch(()=>[]);
    for(const name of events.filter(n=>n.endsWith('.json')).sort()){
      const event=readJSON(path.join(inbox,name),null);
      if(event && Array.isArray(event.files) && event.files.every(p=>typeof p==='string' && path.isAbsolute(p) && fs.existsSync(p))) {
        record({name:event.files.map(p=>path.basename(p)).join(', '),paths:event.files,direction:'received',status:'done',progress:100});
        if(preferences.clipboard && Date.now()-Date.parse(event.receivedAt)<60000)await copyLocalFiles(event.files);
      }
      await fsp.unlink(path.join(inbox,name));
    }
    if(!preferences.clipboard)return;
    const files=await readFiles(clipboard);const signature=JSON.stringify(files);
    if(signature===lastClipboard)return;lastClipboard=signature;
    if(files.length&&connected&&clipboardSupported)await sendFiles(files,true);
  }catch(e){error=e.message;emit();}finally{clipboardBusy=false;}
}
async function poll() {
  if(pollBusy)return;pollBusy=true;
  try{const t0=Date.now();const health=await request('/health');connected=health.status===200 && JSON.parse(health.text).app==='deskbridge';
    if(connected){latency=Date.now()-t0;lastSeen=Date.now();const caps=await request('/capabilities');clipboardSupported=caps.status===200&&!!JSON.parse(caps.text).clipboardFiles;}
    else{latency=null;}
  }catch{connected=false;clipboardSupported=false;latency=null;}finally{pollBusy=false;emit();}
}
function startRelay() {
  if(process.platform==='darwin' && fs.existsSync(path.join(os.homedir(),'Library','LaunchAgents','com.deskbridge.relay.plist'))){
    execFileSync('/bin/launchctl',['kickstart','gui/'+process.getuid()+'/com.deskbridge.relay']);return;
  }
  if(relayProcess||connected)return;
  const cfg=relayConfig();if(!cfg.code)throw Error('Once cihazlari eslestir');
  relayProcess=spawn(binary(),['connect'],{stdio:['ignore','pipe','pipe']});
  relayProcess.on('error',e=>{error=e.message;relayProcess=null;emit();});
  relayProcess.stderr.on('data',data=>{error=data.toString().slice(-500);emit();});
  relayProcess.on('exit',()=>{relayProcess=null;emit();});
}
function readCurrentLayout() {
  try{const conf=fs.readFileSync(settingsFile,'utf8');const name=conf.match(/^computerName=(.+)$/m)?.[1]||os.hostname();layout=readLayout(fs.readFileSync(layoutFile,'utf8'),name);}catch{}
}
async function applyLayout(input) {
  if(relayConfig().side!=='a')throw Error('Ekran duzeni klavye ve mouse bulunan Mac tarafindan yonetilir');
  const text=fs.readFileSync(layoutFile,'utf8');
  const output=changeLayout(text,layout.local,input.peer,input.direction);
  if(!fs.existsSync(layoutFile+'.deskbridge-backup'))fs.copyFileSync(layoutFile,layoutFile+'.deskbridge-backup');
  fs.writeFileSync(layoutFile+'.tmp',output);fs.renameSync(layoutFile+'.tmp',layoutFile);
  layout={...layout,peer:input.peer,direction:input.direction};emit();
  await restartCore();
  return {ok:true,message:'Ekran duzeni uygulandi.'};
}
async function restartCore() {
  const engine=resolveEngine(process.resourcesPath,process.env.DESKBRIDGE_INPUT_BIN);
  if(!engine)throw Error('DeskBridge klavye motoru bulunamadi');
  const mode=relayConfig().side==='a'?'server':'client';
  if(mode==='server'&&!fs.existsSync(layoutFile))throw Error('Ekran yerlesim dosyasi bulunamadi');
  const managed=path.join(configDir,'input.ini');
  fs.mkdirSync(configDir,{recursive:true,mode:0o700});
  fs.writeFileSync(managed,inputSettings(mode,layout.local||os.hostname(),layoutFile),{mode:0o600});
  const serviceFile=path.join(os.homedir(),'Library','LaunchAgents','com.deskbridge.input.plist');
  if(process.platform==='darwin'&&fs.existsSync(serviceFile)){
    const plist=require('plist');const service=plist.parse(fs.readFileSync(serviceFile,'utf8'));
    service.ProgramArguments=[engine,mode,'--settings',managed];
    fs.writeFileSync(serviceFile,plist.build(service),{mode:0o600});
    execFileSync('/bin/launchctl',['bootout','gui/'+process.getuid()+'/com.deskbridge.input']);
    execFileSync('/bin/launchctl',['bootstrap','gui/'+process.getuid(),serviceFile]);
    return;
  }
  if(coreProcess){coreProcess.kill();coreProcess=null;}
  if(mode==='server'){
    let pids=[];
    if(process.platform==='win32'){
      // netstat -ano: columns  Proto  Local  Foreign  State  PID; keep LISTENING on :24800
      try{pids=execFileSync('netstat',['-ano','-p','TCP'],{encoding:'utf8'}).split(/\r?\n/)
        .filter(l=>/LISTENING/i.test(l)&&/[:.]24800\b/.test(l))
        .map(l=>l.trim().split(/\s+/).pop()).filter(Boolean);}catch{}
      for(const pid of pids){
        let image='';
        try{image=execFileSync('tasklist',['/FI','PID eq '+pid,'/FO','CSV','/NH'],{encoding:'utf8'}).trim();}catch{}
        // only kill our own engine (image name = deskbridge-input.exe); never a stranger on the port
        if(!/deskbridge-input\.exe/i.test(image))throw Error('Klavye portu baska bir uygulama tarafindan kullaniliyor');
        try{execFileSync('taskkill',['/PID',pid,'/T','/F']);}catch{}
      }
    }else{
      try{pids=execFileSync('/usr/sbin/lsof',['-t','-iTCP:24800','-sTCP:LISTEN'],{encoding:'utf8'}).trim().split(/\s+/).filter(Boolean);}catch{}
      for(const pid of pids){
        const command=execFileSync('/bin/ps',['-p',pid,'-o','comm='],{encoding:'utf8'}).trim();
        if(command!==engine)throw Error('Klavye portu baska bir uygulama tarafindan kullaniliyor');
        process.kill(Number(pid),'SIGTERM');
      }
    }
    for(let attempt=0;attempt<30;attempt++){
      const inUse=await new Promise(resolve=>{const s=net.connect(24800,'127.0.0.1');s.on('connect',()=>{s.destroy();resolve(true);});s.on('error',()=>resolve(false));s.setTimeout(100,()=>{s.destroy();resolve(true);});});
      if(!inUse)break;
      await new Promise(resolve=>setTimeout(resolve,100));
      if(attempt===29)throw Error('Mevcut klavye oturumu kapanmadi');
    }
  }
  coreProcess=spawn(engine,[mode,'--settings',managed],{stdio:['ignore','pipe','pipe']});
  // tee the input engine's output to a log file so drag-and-drop issues can be
  // diagnosed (on macOS launchd already logs; this covers Linux)
  try{const logFile=path.join(configDir,'input.log');const logStream=fs.createWriteStream(logFile,{flags:'a'});logStream.write('\n--- engine start '+new Date().toISOString()+' ---\n');coreProcess.stdout.pipe(logStream);coreProcess.stderr.pipe(logStream);}catch{}
  coreProcess.on('error',e=>{error=e.message;emit();});
  coreProcess.on('exit',code=>{coreProcess=null;if(code&&!closing){error='Klavye motoru yeniden baslatiliyor ('+code+')';emit();clearTimeout(coreRestartTimer);coreRestartTimer=setTimeout(()=>restartCore().catch(e=>{error=e.message;emit();}),5000);}});
}
function handlers(){
  const handle=(name,fn)=>ipcMain.handle(name,async(event,...args)=>{
    if(event.sender!==win?.webContents)throw Error('Untrusted sender');
    try{return await fn(...args);}catch(e){error=e.message;emit();return {error:e.message};}
  });
  handle('state',()=>snapshot());
  handle('choose-files',async()=>{const result=await dialog.showOpenDialog(win,{properties:['openFile','multiSelections']});if(!result.canceled)return sendFiles(result.filePaths);});
  handle('send-files',files=>sendFiles(files));
  handle('configure',async data=>{
    if(typeof data.clipboard==='boolean'){lastClipboard=JSON.stringify(await readFiles(clipboard));preferences.clipboard=data.clipboard;}
    if(typeof data.autoStart==='boolean'){
      if(process.platform==='darwin'){app.setLoginItemSettings({openAtLogin:data.autoStart});preferences.autoStart=data.autoStart;}
      else if(process.platform==='win32'){
        // Windows autostart via HKCU\...\Run login item, the equivalent of the
        // launchd agent on mac. Electron writes/removes the registry entry.
        app.setLoginItemSettings({openAtLogin:data.autoStart});preferences.autoStart=data.autoStart;
      }
      else {const dir=path.join(process.env.XDG_CONFIG_HOME||path.join(os.homedir(),'.config'),'autostart');fs.mkdirSync(dir,{recursive:true});const file=path.join(dir,'deskbridge.desktop');if(data.autoStart)fs.writeFileSync(file,`[Desktop Entry]\nType=Application\nName=DeskBridge\nExec="${process.execPath}"\nTerminal=false\n`);else if(fs.existsSync(file))fs.unlinkSync(file);preferences.autoStart=data.autoStart;}
    }
    saveJSON(prefsFile,preferences);emit();return {ok:true};
  });
  handle('apply-layout',applyLayout);handle('connect',()=>{startRelay();return {ok:true};});
  handle('open-remote',opts=>{
    if(!connected)throw Error('Diger cihaz bagli degil');
    if(!remote)remote=createRemote({BrowserWindow,ipcMain,iconPath:path.join(__dirname,'icon.png')});
    remote.open(opts&&typeof opts==='object'?opts:{});
    return {ok:true};
  });
  handle('generate-pairing',()=>({code:randomBytes(32).toString('hex')}));
  handle('copy-text',value=>{if(typeof value!=='string'||value.length>256)throw Error('Gecersiz metin');clipboard.writeText(value);return {ok:true};});
  handle('open-relay-setup',()=>shell.openExternal(deployRelay));
  handle('pair',data=>{const code=String(data.code).trim().toLowerCase();const url=String(data.url||defaultRelay).trim().replace(/\/$/,'');let parsed;try{parsed=new URL(url);}catch{}if(!/^[a-f0-9]{64}$/.test(code)||!['a','b'].includes(data.side))throw Error('Eslesme kodu veya cihaz rolu gecersiz');if(!parsed||parsed.protocol!=='https:'||parsed.username||parsed.password||parsed.search||parsed.hash||(parsed.pathname&&parsed.pathname!=='/'))throw Error('Relay adresi gecerli bir HTTPS adresi olmali');saveJSON(relayFile,{url,code,side:data.side});startRelay();return {ok:true};});
  handle('show-file',id=>{const item=transfers.find(t=>t.id===id);if(item)shell.showItemInFolder(item.paths[0]);});
  handle('copy-files',async id=>{const item=transfers.find(t=>t.id===id);if(item)await copyLocalFiles(item.paths);});
  handle('folder',()=>shell.openPath(app.getPath('downloads')));
  ipcMain.on('start-drag',(event,id)=>{const item=transfers.find(t=>t.id===id);if(event.sender===win?.webContents&&item&&fs.existsSync(item.paths[0]))event.sender.startDrag({file:item.paths[0],icon:nativeImage.createFromPath(path.join(__dirname,'icon.png'))});});
}
function showWindow(){if(win){win.show();return;}win=new BrowserWindow({width:1060,height:740,minWidth:760,minHeight:600,title:'DeskBridge',icon:path.join(__dirname,'icon.png'),backgroundColor:'#f5f7f8',webPreferences:{preload:path.join(__dirname,'preload.cjs'),contextIsolation:true,nodeIntegration:false,sandbox:true}});win.loadFile(path.join(__dirname,'index.html'));win.webContents.setWindowOpenHandler(()=>({action:'deny'}));win.webContents.on('will-navigate',e=>e.preventDefault());win.on('close',e=>{if(!closing){e.preventDefault();win.hide();}});}
app.on('second-instance',showWindow);app.on('activate',showWindow);
app.on('before-quit',()=>{closing=true;clearTimeout(coreRestartTimer);relayProcess?.kill();coreProcess?.kill();remote?.destroy();});
app.whenReady().then(async()=>{
  if(process.platform==='darwin')app.dock.setIcon(path.join(__dirname,'icon.png'));
  preferences={...preferences,...readJSON(prefsFile,{})};transfers=readJSON(historyFile,[]);readCurrentLayout();handlers();
  Menu.setApplicationMenu(Menu.buildFromTemplate([{label:'DeskBridge',submenu:[{label:'DeskBridge',click:showWindow},{type:'separator'},{role:'quit'}]},{role:'editMenu'},{role:'windowMenu'}]));
  showWindow();
  const icon=nativeImage.createFromPath(path.join(__dirname,'icon.png')).resize({width:20,height:20});
  tray=new Tray(icon);tray.setToolTip('DeskBridge');tray.setContextMenu(Menu.buildFromTemplate([{label:'DeskBridge',click:showWindow},{label:'Cikis',click:()=>app.quit()}]));tray.on('click',showWindow);
  await poll();if(relayConfig().code){if(!connected)startRelay();await restartCore().catch(e=>{error=e.message;emit();});}
  setInterval(poll,2000);setInterval(pollClipboard,900);
});
