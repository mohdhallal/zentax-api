import * as cdk from 'aws-cdk-lib';
import { Annotations, Match, Template } from 'aws-cdk-lib/assertions';
import { loadDnsConfig } from '../lib/config';
import { dmarcRecordValue, GOOGLE_MX, GOOGLE_SPF, SQUARESPACE_APEX_IPS, SQUARESPACE_WWW_CNAME } from '../lib/dns-stack';
import { cdkJsonContext, exportNamesOf, resourcesOfType, synthDns, synthDnsWith } from './helpers';

const ZONE = 'zentax.software';
const SPF_QUOTED = '"v=spf1 include:_spf.google.com ~all"';
const FAKE_TOKEN = 'AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_AbCdEf';
// 400 characters of base64 alphabet: with the 16-char "v=DKIM1;k=rsa;p=" prefix
// the record value is 416 characters, i.e. two chunks (255 + 161).
const FAKE_DKIM_KEY = 'MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA'.repeat(10).slice(0, 400);
// What lib/config.ts accepts once normalised (the bare token / the bare p= value).
const TOKEN_PATTERN = /^[A-Za-z0-9_-]{8,}$/;
const DKIM_KEY_PATTERN = /^[A-Za-z0-9+/]{64,}={0,2}$/;

/** Route 53 record sets of a template by "<TYPE> <fqdn>" -> properties. */
function recordSets(template: Template): Record<string, any> {
  return Object.fromEntries(
    resourcesOfType(template, 'AWS::Route53::RecordSet').map(([, r]) => [`${r.Properties.Type} ${r.Properties.Name}`, r.Properties]),
  );
}

/**
 * The quoted strings a TXT record value is made of (Route 53 takes a value
 * longer than 255 characters as back-to-back 255-character quoted chunks),
 * checked to be nothing but such chunks, each within the limit.
 */
function txtChunks(value: string): string[] {
  const chunks = value.match(/"[^"]*"/g)!;
  expect(chunks.join('')).toBe(value);
  for (const c of chunks) expect(c.length - 2).toBeLessThanOrEqual(255);
  return chunks.map((c) => c.slice(1, -1));
}

function warningsOf(stack: cdk.Stack): unknown[] {
  return Annotations.fromStack(stack).findWarning('*', Match.anyValue());
}

describe('ZenTax-Dns (account-level hosted zone + records, admin-deployed)', () => {
  // cdk.json as committed. The two Google values are read through the config
  // loader (normalised, validated): the assertions on this synth hold whatever
  // their state — one dedicated test below pins the committed state.
  const { stack, template } = synthDns();
  const committed = loadDnsConfig(new cdk.App({ context: cdkJsonContext() }));
  const committedToken = committed.googleSiteVerification ?? '';
  const committedDkim = committed.googleDkimPublicKey ?? '';
  // Explicit fixtures for the two ends of that state.
  const empty = synthDnsWith({ googleSiteVerification: '', googleDkimPublicKey: '' });
  const full = synthDnsWith({ googleSiteVerification: FAKE_TOKEN, googleDkimPublicKey: FAKE_DKIM_KEY });
  const every = [
    { name: 'committed', stack, template },
    { name: 'empty', ...empty },
    { name: 'full', ...full },
  ];

  test('one public hosted zone for zentax.software, RETAINed on delete, tagged Project=ZenTax, in eu-central-1, CLI-credentials synthesizer', () => {
    expect(stack.stackName).toBe('ZenTax-Dns');
    expect(stack.region).toBe('eu-central-1');
    expect(stack.account).toBe('160117555326');
    template.resourceCountIs('AWS::Route53::HostedZone', 1);
    template.hasResource('AWS::Route53::HostedZone', {
      Properties: Match.objectLike({
        Name: `${ZONE}.`,
        // (CDK renders tags sorted by key.)
        HostedZoneTags: Match.arrayWith([{ Key: 'ManagedBy', Value: 'cdk' }, { Key: 'Project', Value: 'ZenTax' }]),
      }),
      DeletionPolicy: 'Retain',
      UpdateReplacePolicy: 'Retain',
    });
    // Deployed by the admin identity with its own credentials, never through the bootstrap roles.
    expect(template.toJSON().Parameters?.BootstrapVersion).toBeUndefined();
    expect(template.toJSON().Rules?.CheckBootstrapVersion).toBeUndefined();
    // Every record set is in that zone.
    const [zoneId] = resourcesOfType(template, 'AWS::Route53::HostedZone')[0];
    for (const [, r] of resourcesOfType(template, 'AWS::Route53::RecordSet')) {
      expect(r.Properties.HostedZoneId).toEqual({ Ref: zoneId });
    }
  });

  test('the stack is termination-protected and EVERY record set is RETAINed (DeletionPolicy + UpdateReplacePolicy): a stack delete never empties the delegated zone', () => {
    for (const s of every) {
      expect(s.stack.terminationProtection).toBe(true);
      const sets = resourcesOfType(s.template, 'AWS::Route53::RecordSet');
      expect(sets.length).toBeGreaterThanOrEqual(5);
      for (const [id, r] of sets) {
        expect({ id, DeletionPolicy: r.DeletionPolicy, UpdateReplacePolicy: r.UpdateReplacePolicy })
          .toEqual({ id, DeletionPolicy: 'Retain', UpdateReplacePolicy: 'Retain' });
      }
      // Nothing in the stack is deletable with it: the zone and its records are all there is.
      for (const [, r] of Object.entries(s.template.toJSON().Resources as Record<string, any>)) {
        expect(r.DeletionPolicy).toBe('Retain');
        expect(r.UpdateReplacePolicy).toBe('Retain');
      }
    }
  });

  test('A @ = the four Squarespace IPs in ONE record set (panel snapshot 2026-09-05)', () => {
    const a = recordSets(template)[`A ${ZONE}.`];
    expect(a).toBeDefined();
    expect(a.ResourceRecords).toEqual(['198.185.159.144', '198.185.159.145', '198.49.23.144', '198.49.23.145']);
    expect([...SQUARESPACE_APEX_IPS]).toEqual(a.ResourceRecords);
    expect(a.AliasTarget).toBeUndefined();
    expect(Object.keys(recordSets(template)).filter((k) => k.startsWith('A '))).toEqual([`A ${ZONE}.`]);
  });

  test('CNAME www -> ext-sq.squarespace.com', () => {
    const www = recordSets(template)[`CNAME www.${ZONE}.`];
    expect(www).toBeDefined();
    expect(www.ResourceRecords).toEqual(['ext-sq.squarespace.com']);
    expect(SQUARESPACE_WWW_CNAME).toBe('ext-sq.squarespace.com');
  });

  test('MX @ = the five Google Workspace hosts with their priorities', () => {
    const mx = recordSets(template)[`MX ${ZONE}.`];
    expect(mx).toBeDefined();
    expect(mx.ResourceRecords).toEqual([
      '1 aspmx.l.google.com',
      '5 alt1.aspmx.l.google.com',
      '5 alt2.aspmx.l.google.com',
      '10 alt3.aspmx.l.google.com',
      '10 alt4.aspmx.l.google.com',
    ]);
    expect(GOOGLE_MX).toHaveLength(5);
  });

  test('TXT @ is ONE record set (Route 53 allows one TXT set per name): SPF alone while the token is empty, site-verification + SPF once set', () => {
    expect(GOOGLE_SPF).toBe('v=spf1 include:_spf.google.com ~all');
    expect(recordSets(empty.template)[`TXT ${ZONE}.`].ResourceRecords).toEqual([SPF_QUOTED]);
    expect(recordSets(full.template)[`TXT ${ZONE}.`].ResourceRecords).toEqual([
      `"google-site-verification=${FAKE_TOKEN}"`,
      SPF_QUOTED,
    ]);
    // Exactly one TXT record set at the apex, in every state; SPF is always in it.
    for (const s of every) {
      expect(Object.keys(recordSets(s.template)).filter((k) => k === `TXT ${ZONE}.`)).toHaveLength(1);
      expect(resourcesOfType(s.template, 'AWS::Route53::RecordSet').filter(([, r]) => r.Properties.Type === 'TXT' && r.Properties.Name === `${ZONE}.`)).toHaveLength(1);
      expect(recordSets(s.template)[`TXT ${ZONE}.`].ResourceRecords).toContain(SPF_QUOTED);
    }
    // Committed: the token is in the set exactly when cdk.json carries one.
    const apex = recordSets(template)[`TXT ${ZONE}.`].ResourceRecords as string[];
    expect(apex).toEqual(committedToken ? [`"google-site-verification=${committedToken}"`, SPF_QUOTED] : [SPF_QUOTED]);
  });

  test('TXT _dmarc = "v=DMARC1; p=none; rua=mailto:dmarc@zentax.software"', () => {
    expect(dmarcRecordValue(ZONE)).toBe('v=DMARC1; p=none; rua=mailto:dmarc@zentax.software');
    const dmarc = recordSets(template)[`TXT _dmarc.${ZONE}.`];
    expect(dmarc).toBeDefined();
    expect(dmarc.ResourceRecords).toEqual(['"v=DMARC1; p=none; rua=mailto:dmarc@zentax.software"']);
  });

  test('DKIM: google._domainkey is "v=DKIM1;k=rsa;p=<KEY>" emitted as 255-character quoted chunks when the value exceeds 255 characters (proven on the template)', () => {
    const dkim = recordSets(full.template)[`TXT google._domainkey.${ZONE}.`];
    expect(dkim).toBeDefined();
    // One value in the set, made of several quoted strings (that is how Route 53 takes a >255-char TXT value).
    expect(dkim.ResourceRecords).toHaveLength(1);
    const chunks = txtChunks(dkim.ResourceRecords[0]);
    expect(chunks).toHaveLength(2);
    expect(chunks[0]).toHaveLength(255);
    expect(chunks[1]).toHaveLength(416 - 255);
    expect(chunks.join('')).toBe(`v=DKIM1;k=rsa;p=${FAKE_DKIM_KEY}`);
    expect(`v=DKIM1;k=rsa;p=${FAKE_DKIM_KEY}`.length).toBe(416);
    // A short value is a single quoted string.
    expect(txtChunks(recordSets(full.template)[`TXT _dmarc.${ZONE}.`].ResourceRecords[0])).toHaveLength(1);
    // Committed: the record exists exactly when cdk.json carries a key, chunked the same way.
    const committedSet = recordSets(template)[`TXT google._domainkey.${ZONE}.`];
    if (committedDkim) {
      expect(committedSet.ResourceRecords).toHaveLength(1);
      expect(txtChunks(committedSet.ResourceRecords[0]).join('')).toBe(`v=DKIM1;k=rsa;p=${committedDkim}`);
    } else {
      expect(committedSet).toBeUndefined();
    }
  });

  test('empty Google values (explicit fixture): those records are omitted and the stack WARNS — synth succeeds, 5 record sets, SPF only at the apex', () => {
    expect(recordSets(empty.template)[`TXT google._domainkey.${ZONE}.`]).toBeUndefined();
    expect(JSON.stringify(empty.template.toJSON())).not.toContain('google-site-verification');
    expect(Object.keys(recordSets(empty.template))).toHaveLength(5);
    expect(warningsOf(empty.stack)).toHaveLength(2);
    Annotations.fromStack(empty.stack).hasWarning('*', Match.stringLikeRegexp('dns\\.googleSiteVerification is empty'));
    Annotations.fromStack(empty.stack).hasWarning('*', Match.stringLikeRegexp('dns\\.googleDkimPublicKey is empty'));
    Annotations.fromStack(empty.stack).hasNoError('*', Match.anyValue());
    // With both set: no warning at all, and both records present.
    expect(warningsOf(full.stack)).toHaveLength(0);
    expect(recordSets(full.template)[`TXT google._domainkey.${ZONE}.`]).toBeDefined();
    // One value alone -> one warning.
    const tokenOnly = synthDnsWith({ googleSiteVerification: FAKE_TOKEN, googleDkimPublicKey: '' });
    expect(warningsOf(tokenOnly.stack)).toHaveLength(1);
    Annotations.fromStack(tokenOnly.stack).hasWarning('*', Match.stringLikeRegexp('googleDkimPublicKey'));
    expect(Object.keys(recordSets(tokenOnly.template))).toHaveLength(5);
  });

  test('cdk.json as committed, whatever its state: each Google value is "" or valid, warnings = the number of empty values, record sets = 5 + DKIM', () => {
    const dns = cdkJsonContext().dns as Record<string, unknown>;
    expect(dns.zoneName).toBe(ZONE);
    for (const key of ['googleSiteVerification', 'googleDkimPublicKey']) expect(typeof dns[key]).toBe('string');
    // loadDnsConfig already accepted the committed values (it ran at the top); the normalised
    // forms are either absent (empty in cdk.json) or match what the stack renders.
    expect(dns.googleSiteVerification === '' ? '' : committedToken).toMatch(committedToken ? TOKEN_PATTERN : /^$/);
    expect(dns.googleDkimPublicKey === '' ? '' : committedDkim).toMatch(committedDkim ? DKIM_KEY_PATTERN : /^$/);
    expect(!!committedToken).toBe(dns.googleSiteVerification !== '');
    expect(!!committedDkim).toBe(dns.googleDkimPublicKey !== '');
    const missing = [committedToken, committedDkim].filter((v) => !v).length;
    expect(warningsOf(stack)).toHaveLength(missing);
    Annotations.fromStack(stack).hasNoError('*', Match.anyValue());
    expect(Object.keys(recordSets(template))).toHaveLength(5 + (committedDkim ? 1 : 0));
    expect(JSON.stringify(template.toJSON()).includes('google-site-verification')).toBe(!!committedToken);
  });

  test('cdk.json as committed carries the real Google values (pasted 2026-09-05): non-empty, well-formed, and the full-state assertions hold on the committed synth', () => {
    const dns = cdkJsonContext().dns as Record<string, string>;
    expect(dns.googleSiteVerification).not.toBe('');
    expect(dns.googleDkimPublicKey).not.toBe('');
    expect(committedToken).toMatch(TOKEN_PATTERN);
    expect(committedDkim).toMatch(DKIM_KEY_PATTERN);
    // A 2048-bit RSA public key in SPKI DER, base64: the panel truncates it, a
    // complete paste is exactly 392 characters with the standard prefix and the
    // 65537 exponent at the end.
    expect(committedDkim).toMatch(/^MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA/);
    expect(committedDkim).toMatch(/IDAQAB$/);
    expect(committedDkim).toHaveLength(392);
    // No "record omitted" warning, six record sets.
    expect(warningsOf(stack)).toHaveLength(0);
    expect(Object.keys(recordSets(template))).toHaveLength(6);
    // Apex TXT: the token AND SPF in the one set.
    expect(recordSets(template)[`TXT ${ZONE}.`].ResourceRecords).toEqual([
      `"google-site-verification=${committedToken}"`,
      SPF_QUOTED,
    ]);
    // DKIM: one value, 255-character chunks, reassembling to the whole record.
    const dkimValue = `v=DKIM1;k=rsa;p=${committedDkim}`;
    const dkim = recordSets(template)[`TXT google._domainkey.${ZONE}.`];
    expect(dkim.ResourceRecords).toHaveLength(1);
    const chunks = txtChunks(dkim.ResourceRecords[0]);
    expect(chunks).toHaveLength(Math.ceil(dkimValue.length / 255));
    expect(chunks[0]).toHaveLength(255);
    expect(chunks.join('')).toBe(dkimValue);
  });

  test('the whole record value as shown in the Squarespace panel is accepted too (google-site-verification=…, v=DKIM1;k=rsa;p=…), and junk is refused', () => {
    const pasted = synthDnsWith({
      googleSiteVerification: `google-site-verification=${FAKE_TOKEN}`,
      googleDkimPublicKey: `v=DKIM1;k=rsa;p=${FAKE_DKIM_KEY}`,
    });
    expect(recordSets(pasted.template)[`TXT ${ZONE}.`].ResourceRecords[0]).toBe(`"google-site-verification=${FAKE_TOKEN}"`);
    expect(txtChunks(recordSets(pasted.template)[`TXT google._domainkey.${ZONE}.`].ResourceRecords[0]).join('')).toBe(`v=DKIM1;k=rsa;p=${FAKE_DKIM_KEY}`);
    // Whitespace from a wrapped paste is dropped from the key.
    const wrapped = synthDnsWith({ googleDkimPublicKey: `${FAKE_DKIM_KEY.slice(0, 200)}\n ${FAKE_DKIM_KEY.slice(200)}` });
    expect(txtChunks(recordSets(wrapped.template)[`TXT google._domainkey.${ZONE}.`].ResourceRecords[0]).join('')).toBe(`v=DKIM1;k=rsa;p=${FAKE_DKIM_KEY}`);
    expect(() => synthDnsWith({ googleSiteVerification: 'not a token!' })).toThrow(/googleSiteVerification/);
    expect(() => synthDnsWith({ googleDkimPublicKey: 'too-short' })).toThrow(/googleDkimPublicKey/);
    expect(() => synthDnsWith({ zoneName: '' })).toThrow(/zoneName/);
    expect(() => synthDnsWith({ zoneName: 'not a zone' })).toThrow(/zoneName/);
    const noDns = new cdk.App({ context: { ...cdkJsonContext(), dns: undefined } });
    expect(() => loadDnsConfig(noDns)).toThrow(/"dns" must be an object/);
  });

  test('nothing else: no HTTPS/SVCB, no _domainconnect, no cell hostnames (those are the UI app\'s Edge stack), exactly 6 record sets when complete', () => {
    const names = Object.keys(recordSets(full.template)).sort();
    expect(names).toEqual([
      `A ${ZONE}.`,
      `CNAME www.${ZONE}.`,
      `MX ${ZONE}.`,
      `TXT ${ZONE}.`,
      `TXT _dmarc.${ZONE}.`,
      `TXT google._domainkey.${ZONE}.`,
    ].sort());
    expect(names).toHaveLength(6);
    // Without the Google values: 5 (no DKIM set; the apex TXT set still exists, SPF only).
    expect(Object.keys(recordSets(empty.template))).toHaveLength(5);
    // Committed: 5 or 6, never anything but those names.
    expect(Object.keys(recordSets(template)).every((n) => names.includes(n))).toBe(true);
    for (const s of every) {
      const text = JSON.stringify(s.template.toJSON());
      expect(text).not.toContain('_domainconnect');
      expect(text).not.toMatch(/"Type":"(HTTPS|SVCB)"/);
      expect(text).not.toMatch(/eu\.staging|eu\.app|app\.zentax/);
      expect(text).not.toContain('AWS::CertificateManager');
      expect(text).not.toContain('AWS::CloudFront');
      expect(text).not.toContain('AWS::IAM');
      // Not a single AWS lookup at synth: no cdk.context.json is needed (Zone is created, never looked up).
      expect(text).not.toContain('Fn::ImportValue');
      expect(Object.values(s.template.toJSON().Resources as Record<string, any>).every((r) => ['AWS::Route53::HostedZone', 'AWS::Route53::RecordSet'].includes(r.Type))).toBe(true);
    }
  });

  test('outputs HostedZoneId and NameServers (the four NS joined by ", "), as plain outputs — no export the UI app would import', () => {
    template.hasOutput('HostedZoneId', { Value: { Ref: Match.stringLikeRegexp('^Zone') } });
    template.hasOutput('NameServers', {
      Value: { 'Fn::Join': [', ', { 'Fn::GetAtt': [Match.stringLikeRegexp('^Zone'), 'NameServers'] }] },
    });
    expect(exportNamesOf(template)).toEqual([]);
  });
});
