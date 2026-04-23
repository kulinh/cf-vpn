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
  patchNode
} from "./routes/nodes";
import { createUser, deleteUser, listUsers, userSubscription } from "./routes/users";
import { createZone, deleteZone, listZones, patchZone } from "./routes/zones";
import { listEvents } from "./routes/events";

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

function parseZoneName(pathname: string): string {
  const m = pathname.match(/^\/api\/zones\/([^/]+)$/);
  return m ? decodeURIComponent(m[1]) : "";
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);
    const { pathname } = url;

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
      if (request.method === "POST") return createNode(env, request);
    }

    const nodeID = parseNodeID(pathname);
    if (nodeID) {
      if (request.method === "GET") return getNode(env, nodeID);
      if (request.method === "PATCH") return patchNode(env, nodeID, request);
      if (request.method === "DELETE") return deleteNode(env, nodeID);
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
      return nodeRotate(env, nodeRotateID, actor);
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

    if (pathname === "/api/zones") {
      if (request.method === "GET") return listZones(env);
      if (request.method === "POST") return createZone(env, request);
    }

    const zoneName = parseZoneName(pathname);
    if (zoneName) {
      if (request.method === "PATCH") return patchZone(env, zoneName, request);
      if (request.method === "DELETE") return deleteZone(env, zoneName);
    }

    if (pathname === "/api/events" && request.method === "GET") {
      return listEvents(env, url);
    }

    return notFound(pathname);
  }
};
