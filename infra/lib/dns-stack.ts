import * as cdk from 'aws-cdk-lib';
import * as route53 from 'aws-cdk-lib/aws-route53';
import { Construct, IConstruct } from 'constructs';
import { DnsConfig } from './config';

export interface DnsStackProps extends cdk.StackProps {
  readonly cfg: DnsConfig;
}

/**
 * The records recreated from the Squarespace DNS panel (snapshot of
 * 2026-09-05). The registrar stays Squarespace; only the *authoritative* DNS
 * moves to Route 53 (the runbook copies the NameServers output into the
 * Squarespace nameserver settings). The Squarespace HTTPS/SVCB record and the
 * `_domainconnect` CNAME are deliberately not recreated (they exist for
 * Squarespace's own DNS, not for the site).
 */
export const SQUARESPACE_APEX_IPS = ['198.185.159.144', '198.185.159.145', '198.49.23.144', '198.49.23.145'] as const;
export const SQUARESPACE_WWW_CNAME = 'ext-sq.squarespace.com';
export const GOOGLE_MX: ReadonlyArray<{ priority: number; hostName: string }> = [
  { priority: 1, hostName: 'aspmx.l.google.com' },
  { priority: 5, hostName: 'alt1.aspmx.l.google.com' },
  { priority: 5, hostName: 'alt2.aspmx.l.google.com' },
  { priority: 10, hostName: 'alt3.aspmx.l.google.com' },
  { priority: 10, hostName: 'alt4.aspmx.l.google.com' },
];
/** Missing from the Squarespace panel today: SPF for Google Workspace. */
export const GOOGLE_SPF = 'v=spf1 include:_spf.google.com ~all';
/** Missing today: DMARC in monitor-only mode, reports to dmarc@<zone>. */
export function dmarcRecordValue(zoneName: string): string {
  return `v=DMARC1; p=none; rua=mailto:dmarc@${zoneName}`;
}

const TTL = cdk.Duration.hours(1);

/**
 * RETAIN every `AWS::Route53::RecordSet` of the stack (DeletionPolicy and
 * UpdateReplacePolicy). The zone is RETAINed already, but a retained zone
 * whose records are deleted with the stack is just as fatal: the registrar
 * delegates to it, so an empty zone takes the site and the mail down at
 * once. An aspect rather than a call per record, so a record added later
 * cannot forget it (the test suite checks every record set).
 */
class RetainRecordSets implements cdk.IAspect {
  visit(node: IConstruct): void {
    if (cdk.CfnResource.isCfnResource(node) && node.cfnResourceType === route53.CfnRecordSet.CFN_RESOURCE_TYPE_NAME) {
      node.applyRemovalPolicy(cdk.RemovalPolicy.RETAIN);
    }
  }
}

/**
 * ZenTax-Dns: account-level, the public hosted zone for the apex domain and
 * every record it carries — the Squarespace site and Google Workspace mail
 * records copied from the registrar's panel, plus SPF and DMARC. It is
 * deployed by the admin identity with the CLI's own credentials (the app gives
 * it the CliCredentialsStackSynthesizer, like ZenTax-GithubOidc): hosted-zone
 * creation stays out of the pipeline's execution policy on purpose.
 *
 * Nothing in it may disappear by accident: the stack has termination
 * protection on (default; `cdk destroy` refuses until it is switched off in
 * the console/CLI), the zone AND every record set are RETAINed, so even a
 * forced stack delete leaves the delegated zone intact.
 *
 * The cells' own hostnames (eu.staging.<zone>, eu.app.<zone>, app.<zone>) are
 * NOT here: the UI app's Edge stack owns the certificate, the alias records
 * and the entry redirect, writing into this zone by ID (the HostedZoneId
 * output goes into zentax-ui/infra/cdk.json).
 *
 * Route 53 allows one TXT record set per name, so the apex TXT set carries
 * both the Google site-verification token and SPF. TXT values longer than
 * 255 characters (the DKIM key) are emitted as 255-character quoted chunks by
 * CDK's TxtRecord — a test proves it on the synthesized template.
 */
export class DnsStack extends cdk.Stack {
  public readonly zone: route53.PublicHostedZone;

  constructor(scope: Construct, id: string, props: DnsStackProps) {
    // Termination protection on unless a caller says otherwise (the app never does).
    super(scope, id, { ...props, terminationProtection: props.terminationProtection ?? true });
    const { cfg } = props;
    const zoneName = cfg.zoneName;

    this.zone = new route53.PublicHostedZone(this, 'Zone', {
      zoneName,
      comment: `ZenTax: authoritative DNS for ${zoneName} (registrar: Squarespace). Managed by CDK — ZenTax-Dns.`,
    });
    // Deleting the stack must never delete the zone: the name servers are
    // delegated at the registrar, and a recreated zone gets different ones.
    this.zone.applyRemovalPolicy(cdk.RemovalPolicy.RETAIN);
    // ...nor any record in it (see RetainRecordSets).
    cdk.Aspects.of(this).add(new RetainRecordSets());
    // Explicit resource tags (`@aws-cdk/core:explicitStackTags` keeps the
    // stack-level tags off the resources): the zone outlives the stack.
    cdk.Tags.of(this.zone).add('Project', 'ZenTax');
    cdk.Tags.of(this.zone).add('ManagedBy', 'cdk');

    // ---- Squarespace site (panel snapshot 2026-09-05) ------------------------
    new route53.ARecord(this, 'SquarespaceApex', {
      zone: this.zone,
      comment: 'Squarespace site (apex)',
      target: route53.RecordTarget.fromIpAddresses(...SQUARESPACE_APEX_IPS),
      ttl: TTL,
    });
    new route53.CnameRecord(this, 'SquarespaceWww', {
      zone: this.zone,
      recordName: 'www',
      comment: 'Squarespace site (www)',
      domainName: SQUARESPACE_WWW_CNAME,
      ttl: TTL,
    });

    // ---- Google Workspace mail (panel snapshot 2026-09-05 + SPF/DMARC) -------
    new route53.MxRecord(this, 'GoogleMx', {
      zone: this.zone,
      comment: 'Google Workspace mail',
      values: GOOGLE_MX.map((v) => ({ ...v })),
      ttl: TTL,
    });

    const apexTxt: string[] = [];
    if (cfg.googleSiteVerification) {
      apexTxt.push(`google-site-verification=${cfg.googleSiteVerification}`);
    } else {
      cdk.Annotations.of(this).addWarningV2('zentax:dns:google-site-verification-missing',
        'context dns.googleSiteVerification is empty: the google-site-verification TXT record is NOT created. '
        + 'Copy the full value from the Squarespace DNS panel (click the record) into infra/cdk.json before the first deploy of ZenTax-Dns.');
    }
    apexTxt.push(GOOGLE_SPF);
    new route53.TxtRecord(this, 'ApexTxt', {
      zone: this.zone,
      comment: 'Google site verification + SPF (one TXT set per name)',
      values: apexTxt,
      ttl: TTL,
    });

    if (cfg.googleDkimPublicKey) {
      new route53.TxtRecord(this, 'GoogleDkim', {
        zone: this.zone,
        recordName: 'google._domainkey',
        comment: 'Google Workspace DKIM',
        values: [`v=DKIM1;k=rsa;p=${cfg.googleDkimPublicKey}`],
        ttl: TTL,
      });
    } else {
      cdk.Annotations.of(this).addWarningV2('zentax:dns:google-dkim-missing',
        'context dns.googleDkimPublicKey is empty: the google._domainkey DKIM TXT record is NOT created. '
        + 'Copy the full p= value from the Squarespace DNS panel (click the record) into infra/cdk.json before the first deploy of ZenTax-Dns.');
    }

    new route53.TxtRecord(this, 'Dmarc', {
      zone: this.zone,
      recordName: '_dmarc',
      comment: 'DMARC (monitor only)',
      values: [dmarcRecordValue(zoneName)],
      ttl: TTL,
    });

    // ---- Outputs (plain: the UI app reads the zone ID from its own cdk.json) --
    new cdk.CfnOutput(this, 'HostedZoneId', {
      value: this.zone.hostedZoneId,
      description: `Route 53 hosted zone of ${zoneName} — paste into zentax-ui/infra/cdk.json (environments.<cell>.hostedZoneId)`,
    });
    new cdk.CfnOutput(this, 'NameServers', {
      value: cdk.Fn.join(', ', this.zone.hostedZoneNameServers ?? []),
      description: `The four name servers to enter at the registrar (Squarespace) for ${zoneName}`,
    });
  }
}
