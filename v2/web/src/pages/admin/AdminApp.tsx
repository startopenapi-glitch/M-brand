import { lazy, Suspense, useState } from "react";
import {AdminBoundary} from "../../features/admin/shared/AdminBoundary";
import AdminLogin from "./AdminLogin";
import {useAdminSession} from "../../features/admin/auth/useAdminSession";
import {adminResources} from "../../features/admin/app/resources";
import { Routes, Route, NavLink, Navigate, useLocation } from "react-router-dom";
import { ChevronDown } from "lucide-react";
import { BrandMark } from "../../components/SiteLayout";
import { Button } from "../../components/ui/button";

import { api } from "../../lib/api";
const Dashboard=lazy(()=>import("./Dashboard"));
const Codes=lazy(()=>import("./Codes"));
const Jobs=lazy(()=>import("./Jobs"));
const Usage=lazy(()=>import("./Usage"));
const Audit=lazy(()=>import("./Audit"));
const Agents=lazy(()=>import("./Agents"));
const AdminOrders=lazy(()=>import("./AdminOrders"));
const Support=lazy(()=>import("./Support"));

export default function AdminApp() {
  const [navigationOpen, setNavigationOpen] = useState(false);
  const { pathname } = useLocation();
  const {session,status,retry}=useAdminSession();
  const destinations=adminResources.filter(r=>session?.capabilities.includes(r.capability)).map(r=>[r.label,r.path]);
  const activeLabel = destinations.find(([,path]) => path === pathname)?.[0] || "仪表盘";
  if(status==="loading")return <p className="p-16 text-center" role="status">正在连接管理后台…</p>;
  if(status==="error")return <div className="p-8"><p role="alert">后台暂时无法连接，登录状态尚未确认。</p><Button onClick={retry}>重试连接</Button></div>;
  if(status==="denied")return <div className="p-8"><p role="alert">当前账号无后台访问权限。</p><Button onClick={retry}>重试连接</Button></div>;
  if(status==="anonymous")return <AdminLogin onAuthenticated={retry}/>;
  return (
    <div className="zb-admin min-h-screen">
      <header className="workspace-header">
        <div className="mx-auto max-w-[1240px] px-5 py-3 flex items-center gap-5 text-sm">
          <BrandMark /><span className="text-[var(--muted)]">管理后台</span>
          <button className="ml-auto min-h-11 text-[var(--muted)]"
                  onClick={async () => { await api("/api/admin/logout", { method: "POST" }); location.reload(); }}>退出</button>
        </div>
      </header>
      <div className="admin-frame">
      <aside className={`admin-sidebar ${navigationOpen ? "is-open" : ""}`}>
        <p className="eyebrow">管理工作台</p>
        <button className="admin-navigation-toggle" aria-expanded={navigationOpen} aria-controls="admin-navigation" onClick={() => setNavigationOpen(v=>!v)}>后台导航 · {activeLabel}<ChevronDown size={17}/></button>
        <nav id="admin-navigation" aria-label="管理导航">
          {[...new Set(adminResources.map(r=>r.group))].map(group=><div key={group}><p className="text-xs text-[var(--muted)] mt-4 mb-1 px-2">{group}</p>{adminResources.filter(r=>r.group===group&&session?.capabilities.includes(r.capability)).map(r=><NavLink key={r.path} to={r.path} end={r.path==="/admin"} onClick={()=>setNavigationOpen(false)}>{r.label}</NavLink>)}</div>)}
        </nav>
      </aside>
      <main className="admin-content">
        <AdminBoundary key={pathname}><Suspense fallback={<p role="status">正在加载页面…</p>}><Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/orders" element={<AdminOrders />} />
          <Route path="/support" element={<Support />} />
          <Route path="/codes" element={<Codes />} />
          <Route path="/jobs" element={<Jobs />} />
          <Route path="/usage" element={<Usage />} />
          <Route path="/agents" element={<Agents />} />
          <Route path="/audit" element={<Audit />} />
          <Route path="*" element={<Navigate to="/admin" />} />
        </Routes></Suspense></AdminBoundary>
      </main>
      </div>
    </div>
  );
}
