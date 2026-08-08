import type { Env } from "./types";
import { enforceRateLimit, requireActorEmail } from "./lib/auth";
import { notFound } from "./lib/http";
import { handleMe } from "./routes/me";
import {
  createNode,
  deleteNode,
  getNode,
  listNodes,
  nodeHealthcheck,
  nodeRotate,
  nodeStatus,
  nodeSync,
  patchNode,
  sweepNodesHealth
} from "./routes/nodes";
import { createUser, deleteUser, listUsers, userSubscription, userUpgradeNodes } from "./routes/users";
import { createZone, deleteZone, listZones, patchZone } from "./routes/zones";
import { listEvents } from "./routes/events";
import { publicSubscription } from "./routes/sub";
import { handleTelegramWebhook } from "./routes/telegram";

function parseNodeID(pathname: string): string {
  const m = pathname.match(/^\/api\/nodes\/([^/]+)$/);
  return m ? decodeURIComponent(m[1]) : "";
}

function parseNodeAction(pathname: string, action: string): string {
  const m = pathname.match(new RegExp(`^/api/nodes/([^/]+)/${action}$`));
  return m ? decodeURIComponent(m[1]) : "";
}

function parseUserID(pathname: string): string {
  const m = pathname.match(/^\/api\/users\/([^/]+)$/);
  return m ? decodeURIComponent(m[1]) : "";
}

function parseUserSubscriptionID(pathname: string): string {
  const m = pathname.match(/^\/api\/users\/([^/]+)\/subscription$/);
  return m ? decodeURIComponent(m[1]) : "";
}

function parseUserUpgradeNodesID(pathname: string): string {
  const m = pathname.match(/^\/api\/users\/([^/]+)\/upgrade-nodes$/);
  return m ? decodeURIComponent(m[1]) : "";
}

function parseZoneName(pathname: string): string {
  const m = pathname.match(/^\/api\/zones\/([^/]+)$/);
  return m ? decodeURIComponent(m[1]) : "";
}

function parseSubToken(pathname: string): string {
  const m = pathname.match(/^\/sub\/([^/]+)$/);
  return m ? decodeURIComponent(m[1]) : "";
}

export default {
  async fetch(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    const url = new URL(request.url);
    const { pathname } = url;

    // The *.workers.dev hostname does NOT sit behind Cloudflare Access, so the
    // Cf-Access-* headers on it are forgeable. It must stay reachable only for
    // the Telegram webhook (registered against workers.dev because the Access
    // custom domain doesn't route that path). Reject everything else here so an
    // attacker can't reach /api/* or /sub/* with spoofed Access headers.
    if (url.hostname.endsWith(".workers.dev") && pathname !== "/telegram/webhook") {
      return notFound(pathname);
    }

    const subToken = parseSubToken(pathname);
    if (subToken && request.method === "GET") {
      // Unauthenticated (public) endpoint — rate-limit by source IP so a leaked
      // token can't be brute-forced / scraped at high volume.
      const ipLimited = enforceRateLimit(`sub:${request.headers.get("CF-Connecting-IP") ?? "unknown"}`);
      if (ipLimited) return ipLimited;
      return publicSubscription(env, subToken);
    }

    if (pathname === "/telegram/webhook" && request.method === "POST") {
      // Unauthenticated (Access-bypassing) endpoint — rate-limit by source IP.
      const ipLimited = enforceRateLimit(`tg:${request.headers.get("CF-Connecting-IP") ?? "unknown"}`);
      if (ipLimited) return ipLimited;
      return handleTelegramWebhook(env, request, ctx);
    }

    if (!pathname.startsWith("/api/")) {
      return notFound(pathname);
    }

    const actor = requireActorEmail(request);
    if (actor instanceof Response) {
      return actor;
    }
    const limited = enforceRateLimit(actor);
    if (limited) {
      return limited;
    }

    if (pathname === "/api/me" && request.method === "GET") {
      return handleMe(actor);
    }

    if (pathname === "/api/nodes") {
      if (request.method === "GET") return listNodes(env);
      if (request.method === "POST") return createNode(env, request, actor);
    }

    const nodeID = parseNodeID(pathname);
    if (nodeID) {
      if (request.method === "GET") return getNode(env, nodeID);
      if (request.method === "PATCH") return patchNode(env, nodeID, request, actor);
      if (request.method === "DELETE") return deleteNode(env, nodeID, actor);
    }

    const nodeStatusID = parseNodeAction(pathname, "status");
    if (nodeStatusID && request.method === "GET") {
      return nodeStatus(env, nodeStatusID, actor);
    }

    const nodeHealthID = parseNodeAction(pathname, "healthcheck");
    if (nodeHealthID && request.method === "POST") {
      return nodeHealthcheck(env, nodeHealthID, actor);
    }

    const nodeRotateID = parseNodeAction(pathname, "rotate");
    if (nodeRotateID && request.method === "POST") {
      return nodeRotate(env, nodeRotateID, request, actor);
    }

    const nodeSyncID = parseNodeAction(pathname, "sync");
    if (nodeSyncID && request.method === "POST") {
      return nodeSync(env, nodeSyncID, request, actor);
    }

    if (pathname === "/api/users") {
      if (request.method === "GET") return listUsers(env);
      if (request.method === "POST") return createUser(env, request, actor);
    }

    const userID = parseUserID(pathname);
    if (userID && request.method === "DELETE") {
      return deleteUser(env, userID, actor);
    }

    const userSubscriptionID = parseUserSubscriptionID(pathname);
    if (userSubscriptionID && request.method === "GET") {
      return userSubscription(env, userSubscriptionID);
    }

    const userUpgradeNodesID = parseUserUpgradeNodesID(pathname);
    if (userUpgradeNodesID && request.method === "POST") {
      return userUpgradeNodes(env, userUpgradeNodesID, actor);
    }

    if (pathname === "/api/zones") {
      if (request.method === "GET") return listZones(env);
      if (request.method === "POST") return createZone(env, request, actor);
    }

    const zoneName = parseZoneName(pathname);
    if (zoneName) {
      if (request.method === "PATCH") return patchZone(env, zoneName, request, actor);
      if (request.method === "DELETE") return deleteZone(env, zoneName, actor);
    }

    if (pathname === "/api/events" && request.method === "GET") {
      return listEvents(env, url);
    }

    return notFound(pathname);
  },

  async scheduled(event: ScheduledEvent, env: Env, _ctx: ExecutionContext): Promise<void> {
    if (event.cron === "17 3 * * *") {
      const cutoff = Date.now() - 90 * 24 * 60 * 60 * 1000;
      await env.DB.prepare("DELETE FROM events WHERE ts < ?").bind(cutoff).run();
      return;
    }
    await sweepNodesHealth(env);
  }
};
