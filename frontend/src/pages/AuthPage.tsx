import type { ReactNode } from 'react'

/** Centered frame for Clerk's sign-in and sign-up components. */
export function AuthPage({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-dvh flex-col items-center justify-center gap-6 px-4 py-10">
      <div className="text-center">
        <p className="text-2xl font-bold tracking-tight text-emerald-800 dark:text-emerald-400">NMS</p>
        <p className="text-sm text-stone-500">Nursery Management System</p>
      </div>
      {children}
      <p className="max-w-sm text-center text-xs text-stone-500">NMS is invite-only. Use the email address your invitation was sent to.</p>
    </div>
  )
}
