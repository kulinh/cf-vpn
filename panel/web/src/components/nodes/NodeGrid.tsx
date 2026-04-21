import type { Node } from '../../lib/types'
import { NodeCard } from './NodeCard'

type NodeGridProps = {
  nodes: Node[]
  onRotate: (id: string) => void
  onHealthcheck: (id: string) => void
  onOpen: (id: string) => void
}

export function NodeGrid({ nodes, onRotate, onHealthcheck, onOpen }: NodeGridProps) {
  return (
    <section className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
      {nodes.map((node) => (
        <NodeCard
          key={node.id}
          node={node}
          onRotate={onRotate}
          onHealthcheck={onHealthcheck}
          onOpen={onOpen}
        />
      ))}
    </section>
  )
}
