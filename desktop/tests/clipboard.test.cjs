const test=require('node:test');const assert=require('node:assert/strict');
const {readFiles,fileItem}=require('../clipboard.cjs');
class Item {constructor(values){this.values=values;this.types=Object.keys(values);}async getType(type){return this.values[type];}}
// These encode mac/linux clipboard file-reference formats (POSIX paths + file://
// URLs); a Windows node uses CF_HDROP, so skip them off-POSIX where path/URL
// handling would mangle the fixtures.
const posixOnly={skip:process.platform==='win32'&&'POSIX clipboard format'};
test('Mac native file references preserve spaces and multiple paths',posixOnly,async()=>{const files=['/tmp/a b.txt','/tmp/second.txt'];const item=fileItem(files,Item,'darwin');assert.deepEqual(await readFiles({read:async()=>[item]}),files);});
test('Linux file clipboard roundtrip',posixOnly,async()=>{const files=['/tmp/a b.txt','/tmp/z.txt'];assert.deepEqual(await readFiles({read:async()=>[fileItem(files,Item,'linux')]}),files);});
test('ordinary text is never interpreted as files',async()=>{assert.deepEqual(await readFiles({read:async()=>[new Item({'text/plain':new Blob(['file:///private/file'])})]}),[]);});
