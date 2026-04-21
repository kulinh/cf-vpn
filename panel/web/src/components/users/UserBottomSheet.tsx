import type { ReactNode } from 'react'

type UserBottomSheetProps = {
  open: boolean
  onClose: () => void
  children: ReactNode
}

export function UserBottomSheet({ open, onClose, children }: UserBottomSheetProps) {
  if (!open) {
    return null
  }

  return (
    <div className="fixed inset-0 z-50 bg-black/40 lg:hidden" onClick={onClose}>
      <div
        className="absolute bottom-0 left-0 right-0 rounded-t-2xl border border-slate-700 bg-slate-900 p-4"
        onClick={(event) => event.stopPropagation()}
      >
        <div className="mx-auto mb-3 h-1.5 w-12 rounded-full bg-slate-600" aria-hidden="true" />
        {children}
      </div>
    </div>
  )
}
