import { useEffect, useState } from "react";
import { Routes, Route, Navigate, useLocation } from "react-router-dom";
import { BrandMark } from "../../components/SiteLayout";
import { api } from "../../lib/api";
import AgentDashboard from "./Dashboard";
import AgentLogin from "./Login";
import AgentInvite from "./Invite";

export default function AgentApp() {
  const [authed, setAuthed] = useState<boolean | null>(null);
  const { pathname } = useLocation();
  const inviteToken = pathname.match(/^\/agent\/invite\/([^/]+)$/)?.[1] || "";

  useEffect(() => {
    api("/api/agent/me/stats").then(() => setAuthed(true)).catch(() => setAuthed(false));
  }, []);

  if (authed === null) return <p className="p-16 text-center text-[var(--muted)]">…</p>;
  if (!authed && inviteToken) return <AgentInvite token={decodeURIComponent(inviteToken)} onDone={() => setAuthed(true)} />;
  if (!authed) return <AgentLogin onLogin={() => setAuthed(true)} />;

  return (
    <div className="zb-agent min-h-screen">
      <header className="workspace-header">
        <div className="mx-auto max-w-[800px] px-5 py-3 flex items-center justify-between">
          <div className="flex items-center gap-4"><BrandMark /><span className="text-sm text-[var(--muted)]">代理工作台</span></div>
          <button className="min-h-11 text-[var(--muted)] text-sm"
            onClick={async () => {
              await api("/api/agent/logout", { method: "POST" });
              location.reload();
            }}>退出</button>
        </div>
      </header>
      <main className="mx-auto max-w-[800px] px-5 py-8">
        <Routes>
          <Route path="/" element={<AgentDashboard />} />
          <Route path="*" element={<Navigate to="/agent" />} />
        </Routes>
      </main>
    </div>
  );
}
