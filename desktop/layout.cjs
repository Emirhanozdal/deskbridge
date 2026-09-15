const fs = require('node:fs');
const directions = ['left', 'right', 'up', 'down'];
const opposite = { left: 'right', right: 'left', up: 'down', down: 'up' };
function readLayout(text, local) {
  const screens = []; let section = '', current = ''; const links = {};
  for (const line of text.split(/\r?\n/)) {
    const heading = line.match(/^section:\s*(\w+)/);
    if (heading) { section = heading[1]; continue; }
    if (line.trim() === 'end') { section = ''; continue; }
    const name = line.match(/^\s*([^\s:=]+):\s*$/);
    if (name) { current = name[1]; if (section === 'screens') screens.push(current); }
    if (section === 'links') {
      const link = line.match(/^\s*(left|right|up|down)\s*=\s*([^\s]+)\s*$/);
      if (link) (links[current] ||= {})[link[1]] = link[2];
    }
  }
  const direction = Object.keys(links[local] || {})[0] || 'right';
  return { local, peer: links[local]?.[direction] || screens.find(n => n !== local) || 'linux-pc', direction, screens };
}
function changeLayout(text, local, peer, direction) {
  if (!directions.includes(direction) || local === peer || ![local, peer].every(n => /^[\w.-]+$/.test(n))) throw Error('Gecersiz cihaz duzeni');
  const parsed = readLayout(text, local);
  if (![local, peer].every(n => parsed.screens.includes(n))) throw Error('Cihaz adi mevcut duzende bulunamadi');
  const block = `section: links\n\t${local}:\n\t\t${direction} = ${peer}\n\t${peer}:\n\t\t${opposite[direction]} = ${local}\nend`;
  if (!/^section:\s*links\s*$[\s\S]*?^end\s*$/m.test(text)) throw Error('Mevcut ekran duzeni okunamadi');
  return text.replace(/^section:\s*links\s*$[\s\S]*?^end\s*$/m, block);
}
module.exports = { readLayout, changeLayout };
