const { app, clipboard, ClipboardItem } = require('electron');
const fs=require('node:fs');const path=require('node:path');const os=require('node:os');const assert=require('node:assert/strict');
const {readFiles,fileItem}=require('../clipboard.cjs');
app.whenReady().then(async()=>{
  let previous;
  const dir=fs.mkdtempSync(path.join(os.tmpdir(),'deskbridge-clipboard-'));
  try{
    previous=await Promise.all((await clipboard.read()).map(async item=>{
      const values={};for(const type of item.types)values[type]=await item.getType(type);
      return new ClipboardItem(values);
    }));
    const files=[path.join(dir,'file one.txt'),path.join(dir,'file-two.txt')];files.forEach(p=>fs.writeFileSync(p,'test'));
    await clipboard.write([fileItem(files,ClipboardItem)]);
    assert.deepEqual(await readFiles(clipboard),files);
    console.log('Native file clipboard roundtrip passed.');
  }catch(error){console.error(error);process.exitCode=1;}
  finally{try{if(previous)await clipboard.write(previous);}catch(e){console.error('Clipboard restore failed:',e.message);process.exitCode=1;}fs.rmSync(dir,{recursive:true,force:true});app.exit(process.exitCode||0);}
});
