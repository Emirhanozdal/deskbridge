const test=require('node:test');const assert=require('node:assert/strict');
const {readLayout,changeLayout}=require('../layout.cjs');
const layout='section: screens\n Mac:\n emirh:\nend\nsection: links\n Mac:\n  left = emirh\n emirh:\n  right = Mac\nend\nsection: options\n clipboardSharing = true\nend\n';
test('imports active peer and preserves unrelated config',()=>{assert.equal(readLayout(layout,'Mac').peer,'emirh');const changed=changeLayout(layout,'Mac','emirh','up');assert.equal(readLayout(changed,'Mac').direction,'up');assert.ok(changed.includes('clipboardSharing = true'));assert.ok(changed.includes('down = Mac'));});
test('rejects invalid or missing devices',()=>{assert.throws(()=>changeLayout(layout,'Mac','emirh\nend','left'));assert.throws(()=>changeLayout(layout,'Mac','missing','left'));assert.throws(()=>changeLayout(layout,'Mac','emirh','diagonal'));});
