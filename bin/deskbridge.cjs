#!/usr/bin/env node
'use strict';

const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const crypto = require('node:crypto');
const { spawnSync } = require('node:child_process');

const version = '0.2.0';
const releases = {
  'darwin-arm64': 'dafea8a1e0d5cbee35e43ca724241593952036cd81eb3f66dd0ea8a258136a24',
  'linux-x64': 'a6e34649552037c8756d6471a68bf54b78902cdc3ef9db5c50921df204ccf38f',
};
const cloudflared = {
  'darwin-arm64': ['cloudflared-darwin-arm64.tgz', 'c27ab8fd0aa489449e3d201eb02f957ef460a13b613662928b1b23394bf1bcfe'],
  'linux-x64': ['cloudflared-linux-amd64', '03f1f25d1cc93b9ad6c60569d44060bc4f17ed97075760ed8cfca4b12dcd68cc'],
};

function run(command, args) {
  const result = spawnSync(command, args, { stdio: 'inherit' });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} failed (${result.signal || result.status})`);
}

try {
  const platform = `${process.platform}-${process.arch}`;
  const checksum = releases[platform];
  if (!checksum) throw new Error(`Unsupported platform: ${platform}. Available: macOS Apple Silicon, Linux x86_64.`);
  const cache = path.join(process.env.XDG_CACHE_HOME || path.join(os.homedir(), '.cache'), 'deskbridge', version, platform);
  const binary = path.join(cache, 'deskbridge');
  if (!fs.existsSync(binary)) {
    fs.mkdirSync(cache, { recursive: true, mode: 0o700 });
    const staging = fs.mkdtempSync(path.join(cache, 'download-'));
    try {
      const arch = process.arch === 'x64' ? 'amd64' : process.arch;
      const asset = `deskbridge-${version}-${process.platform}-${arch}.tar.gz`;
      const archive = path.join(staging, asset);
      console.error(`Downloading DeskBridge ${version}...`);
      run('curl', ['-fL', '--retry', '5', '--connect-timeout', '15', '--max-time', '300',
        `https://github.com/Emirhanozdal/deskbridge/releases/download/v${version}/${asset}`, '-o', archive]);
      const actual = crypto.createHash('sha256').update(fs.readFileSync(archive)).digest('hex');
      if (actual !== checksum) throw new Error('Download checksum mismatch. Refusing to run it.');
      run('tar', ['-xzf', archive, '-C', staging, 'deskbridge']);
      fs.chmodSync(path.join(staging, 'deskbridge'), 0o755);
      fs.renameSync(path.join(staging, 'deskbridge'), binary);
    } finally {
      fs.rmSync(staging, { recursive: true, force: true });
    }
  }
  const args = process.argv.slice(2);
  if (args.includes('tunnel') && !args.some(arg => ['help', '--help', '-h'].includes(arg)) && !fs.existsSync(path.join(cache, 'cloudflared'))) {
    const staging = fs.mkdtempSync(path.join(cache, 'cloudflared-'));
    try {
      const [asset, expected] = cloudflared[platform];
      const download = path.join(staging, asset);
      console.error('Downloading cloudflared 2026.9.1...');
      run('curl', ['-fL', '--retry', '5', '--connect-timeout', '15', '--max-time', '300',
        `https://github.com/cloudflare/cloudflared/releases/download/2026.9.1/${asset}`, '-o', download]);
      const actual = crypto.createHash('sha256').update(fs.readFileSync(download)).digest('hex');
      if (actual !== expected) throw new Error('cloudflared checksum mismatch. Refusing to run it.');
      let extracted = download;
      if (asset.endsWith('.tgz')) {
        run('tar', ['-xzf', download, '-C', staging, 'cloudflared']);
        extracted = path.join(staging, 'cloudflared');
      }
      fs.chmodSync(extracted, 0o755);
      fs.renameSync(extracted, path.join(cache, 'cloudflared'));
    } finally {
      fs.rmSync(staging, { recursive: true, force: true });
    }
  }
  const result = spawnSync(binary, args, {
    stdio: 'inherit', env: { ...process.env, PATH: cache + path.delimiter + (process.env.PATH || '') },
  });
  if (result.error) throw result.error;
  process.exitCode = result.status === null ? 1 : result.status;
} catch (error) {
  console.error(`deskbridge: ${error.message}`);
  process.exitCode = 1;
}
