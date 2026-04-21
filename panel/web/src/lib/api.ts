export type RotateNodeResponse = {
  vpnHost: string
}

export async function rotateNode(nodeId: string): Promise<RotateNodeResponse> {
  const response = await fetch(`/api/nodes/${encodeURIComponent(nodeId)}/rotate`, {
    method: 'POST',
  })

  if (!response.ok) {
    throw new Error('rotate failed')
  }

  return (await response.json()) as RotateNodeResponse
}
