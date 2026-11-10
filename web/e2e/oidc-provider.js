// A minimal OpenID Connect provider for the OIDC e2e spec: discovery, keys,
// an authorization endpoint that approves every request at once (no login
// page), and a token endpoint that checks the client, the code and its PKCE
// verifier, then issues an RS256 ID token carrying the request's nonce.
import { createHash, createSign, generateKeyPairSync, randomBytes } from 'node:crypto';
import { once } from 'node:events';
import http from 'node:http';

const b64url = (buf) => Buffer.from(buf).toString('base64url');

export class Provider {
  constructor({ clientId, clientSecret }) {
    this.clientId = clientId;
    this.clientSecret = clientSecret;
    const { privateKey, publicKey } = generateKeyPairSync('rsa', { modulusLength: 2048 });
    this.privateKey = privateKey;
    this.jwk = { ...publicKey.export({ format: 'jwk' }), kid: 'e2e', alg: 'RS256', use: 'sig' };
    this.codes = new Map();
    // Number of authorization requests seen: each is one login round trip.
    this.logins = 0;
    this.server = http.createServer((req, res) => this.handle(req, res));
  }

  async start() {
    this.server.listen(0, '127.0.0.1');
    await once(this.server, 'listening');
    this.issuer = `http://127.0.0.1:${this.server.address().port}`;
  }

  async stop() {
    this.server.close();
    await once(this.server, 'close');
  }

  sign(claims) {
    const header = b64url(JSON.stringify({ alg: 'RS256', typ: 'JWT', kid: 'e2e' }));
    const body = b64url(JSON.stringify(claims));
    const sig = createSign('RSA-SHA256').update(`${header}.${body}`).sign(this.privateKey);
    return `${header}.${body}.${b64url(sig)}`;
  }

  // An access token for API clients, as a client credentials grant with a
  // resource would get from a provider such as Pocket ID.
  accessToken(aud) {
    const now = Math.floor(Date.now() / 1000);
    return this.sign({ iss: this.issuer, sub: 'e2e-client', aud, iat: now, exp: now + 3600 });
  }

  handle(req, res) {
    const url = new URL(req.url, this.issuer);
    const json = (status, body) => {
      res.writeHead(status, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify(body));
    };
    switch (url.pathname) {
      case '/.well-known/openid-configuration':
        return json(200, {
          issuer: this.issuer,
          authorization_endpoint: `${this.issuer}/auth`,
          token_endpoint: `${this.issuer}/token`,
          jwks_uri: `${this.issuer}/keys`,
          response_types_supported: ['code'],
          subject_types_supported: ['public'],
          id_token_signing_alg_values_supported: ['RS256'],
        });
      case '/keys':
        return json(200, { keys: [this.jwk] });
      case '/auth': {
        this.logins++;
        const q = url.searchParams;
        const code = randomBytes(16).toString('hex');
        this.codes.set(code, {
          challenge: q.get('code_challenge'),
          nonce: q.get('nonce'),
          redirectUri: q.get('redirect_uri'),
        });
        const back = new URL(q.get('redirect_uri'));
        back.searchParams.set('code', code);
        back.searchParams.set('state', q.get('state'));
        res.writeHead(302, { Location: back.toString() });
        return res.end();
      }
      case '/token': {
        let raw = '';
        req.on('data', (chunk) => (raw += chunk));
        req.on('end', () => {
          const form = new URLSearchParams(raw);
          const [id, secret] = Buffer.from((req.headers.authorization || '').replace(/^Basic /, ''), 'base64')
            .toString()
            .split(':')
            .map(decodeURIComponent);
          if (id !== this.clientId || secret !== this.clientSecret) return json(401, { error: 'invalid_client' });
          const pending = this.codes.get(form.get('code'));
          this.codes.delete(form.get('code'));
          const challenge = b64url(createHash('sha256').update(form.get('code_verifier') || '').digest());
          if (!pending || pending.challenge !== challenge || pending.redirectUri !== form.get('redirect_uri')) {
            return json(400, { error: 'invalid_grant' });
          }
          const now = Math.floor(Date.now() / 1000);
          json(200, {
            access_token: 'opaque',
            token_type: 'Bearer',
            expires_in: 3600,
            id_token: this.sign({
              iss: this.issuer,
              sub: 'e2e-user',
              aud: this.clientId,
              nonce: pending.nonce,
              iat: now,
              exp: now + 3600,
            }),
          });
        });
        return;
      }
      default:
        res.writeHead(404);
        res.end();
    }
  }
}
