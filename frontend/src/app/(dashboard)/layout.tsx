import { Sidebar } from '@/components/Sidebar'

export default function DashboardLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-screen overflow-hidden" style={{ background: 'var(--paper)' }}>
      <Sidebar />
      <main className="dashboard-shell flex-1 overflow-auto">
        {children}
      </main>
    </div>
  )
}
