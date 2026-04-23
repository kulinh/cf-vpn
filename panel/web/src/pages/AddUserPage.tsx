import { FormEvent, useState } from 'react'
import { createUser } from '../lib/api'

export function AddUserPage() {
  const [name, setName] = useState('')
  const [message, setMessage] = useState<string | null>(null)

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    await createUser({ name })
    setMessage('User added')
    setName('')
  }

  return (
    <section className="space-y-4">
      <h1 className="text-xl font-semibold">Add user</h1>
      <form className="space-y-3 rounded-lg border border-slate-800 bg-slate-900 p-4" onSubmit={(event) => void handleSubmit(event)}>
        <label className="block text-sm text-slate-200">
          Name
          <input
            className="mt-1 w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 text-slate-100"
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <button type="submit" className="rounded bg-indigo-500 px-3 py-1.5 text-sm font-medium text-white">
          Add user
        </button>
      </form>
      {message ? <p className="text-sm text-green-400">{message}</p> : null}
    </section>
  )
}
