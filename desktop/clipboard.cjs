const { fileURLToPath, pathToFileURL } = require('node:url');
const plist = require('plist');
const raw = name => `electron application/osclipboard;format="${name}"`;
async function readFiles(clipboard) {
  const found = [];
  for (const item of await clipboard.read()) {
    for (const type of item.types) {
      if (!['public.file-url', 'NSFilenamesPboardType', 'text/uri-list', 'x-special/gnome-copied-files'].some(n => type.includes(n))) continue;
      const text = await (await item.getType(type)).text();
      if (type.includes('NSFilenamesPboardType') && text.includes('<plist')) {
        try { const files = plist.parse(text); if (Array.isArray(files)) found.push(...files.filter(p => typeof p === 'string')); } catch {}
      } else {
        for (const line of text.split(/[\r\n\0]+/)) {
          if (line.startsWith('file:')) { try { found.push(fileURLToPath(line)); } catch {} }
        }
      }
    }
  }
  return [...new Set(found)].slice(0,100);
}
function fileItem(files, ClipboardItem, platform = process.platform) {
  const uris = files.map(p => pathToFileURL(p).href);
  if (platform === 'darwin') return new ClipboardItem({
    [raw('NSFilenamesPboardType')]: new Blob([plist.build(files)]),
    [raw('public.file-url')]: new Blob([uris[0]]),
  });
  return new ClipboardItem({
    'text/uri-list': new Blob([uris.join('\r\n')]),
    [raw('x-special/gnome-copied-files')]: new Blob(['copy\n' + uris.join('\n')]),
  });
}
module.exports = { readFiles, fileItem };
