import { json } from "../lib/http";

export function handleMe(actor: string): Response {
  return json({ email: actor });
}
