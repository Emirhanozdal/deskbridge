const test=require('node:test');const assert=require('node:assert/strict');
const {readFiles,fileItem}=require('../clipboard.cjs');
class Item {constructor(values){this.values=values;this.types=Object.keys(values);}async getType(type){return this.values[type];}}
test('Mac native file references preserve spaces and multiple paths',async()=>{const files=['/tmp/a b.txt','/tmp/second.txt'];const item=fileItem(files,Item,'darwin');assert.deepEqual(await readFiles({read:async()=>[item]}),files);});
test('Linux file clipboard roundtrip',async()=>{const files=['/tmp/a b.txt','/tmp/z.txt'];assert.deepEqual(await readFiles({read:async()=>[fileItem(files,Item,'linux')]}),files);});
test('ordinary text is never interpreted as files',async()=>{assert.deepEqual(await readFiles({read:async()=>[new Item({'text/plain':new Blob(['file:///private/file'])})]}),[]);});
