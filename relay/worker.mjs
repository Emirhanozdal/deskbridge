export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    if (url.pathname === '/health') return Response.json({ service: 'deskbridge-relay', protocol: 1 });
    if (!env.AUTH_HASH) return new Response('Not configured', { status: 503 });
    if (request.headers.get('Authorization') !== `Bearer ${env.AUTH_HASH}`) {
      return new Response('Unauthorized', { status: 401 });
    }
    if (!/^\/connect\/(a|b)$/.test(url.pathname)) return new Response('Not found', { status: 404 });
    if (request.headers.get('Upgrade')?.toLowerCase() !== 'websocket') return new Response('WebSocket required', { status: 426 });
    return env.PAIR.get(env.PAIR.idFromName('personal-pair')).fetch(request);
  }
};

export class Pair {
  constructor(ctx) { this.ctx = ctx; }
  async fetch(request) {
    const side = new URL(request.url).pathname.split('/').pop();
    if (this.ctx.getWebSockets(side).length) return new Response('This device is already connected', { status: 409 });
    const [client, server] = Object.values(new WebSocketPair());
    this.ctx.acceptWebSocket(server, [side]);
    const peers = this.ctx.getWebSockets(side === 'a' ? 'b' : 'a');
    if (peers.length) {
      peers[0].send('ready');
      server.send('ready');
    }
    return new Response(null, { status: 101, webSocket: client });
  }
  webSocketMessage(socket, message) {
    if (typeof message === 'string' || message.byteLength > 1048576) {
      socket.close(1008, 'Invalid frame');
      return;
    }
    const side = this.ctx.getTags(socket)[0];
    const peer = this.ctx.getWebSockets(side === 'a' ? 'b' : 'a')[0];
    if (!peer) { socket.close(1011, 'Peer disconnected'); return; }
    try { peer.send(message); } catch { socket.close(1011, 'Peer disconnected'); }
  }
  webSocketClose(socket) { this.closePair(socket); }
  webSocketError(socket) { this.closePair(socket); }
  closePair(socket) {
    for (const peer of this.ctx.getWebSockets()) {
      try { peer.close(1000, 'Pair disconnected'); } catch {}
    }
    try { socket.close(1000, 'Disconnected'); } catch {}
  }
}
