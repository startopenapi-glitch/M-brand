import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import { lazy, Suspense } from "react";
import SiteLayout from "./components/SiteLayout";
import Home from "./pages/Home";
const Wizard = lazy(() => import("./pages/Wizard"));
const JobPage = lazy(() => import("./pages/Job"));
const Gallery = lazy(() => import("./pages/Gallery"));
const Pricing = lazy(() => import("./pages/Pricing"));
const PayPage = lazy(() => import("./pages/Pay"));
const Orders = lazy(() => import("./pages/Orders"));
const Redeem = lazy(() => import("./pages/Redeem"));
const AdminApp = lazy(() => import("./pages/admin/AdminApp"));
const AgentApp = lazy(() => import("./pages/agent/AgentApp"));

const Loading = () => (
  <div style={{ display: "grid", placeItems: "center", minHeight: "50vh", color: "#999" }}>加载中…</div>
);
export default function App() {
  return (
    <BrowserRouter>
      <Suspense fallback={<Loading />}><Routes>
        <Route element={<SiteLayout />}>
        <Route path="/" element={<Home />} />
        <Route path="/create" element={<Wizard />} />
        <Route path="/job/:id" element={<JobPage />} />
        <Route path="/gallery" element={<Gallery />} />
        <Route path="/pricing" element={<Pricing />} />
        <Route path="/pay" element={<PayPage />} />
        <Route path="/orders" element={<Orders />} />
        <Route path="/redeem" element={<Redeem />} />
        </Route>
        <Route path="/agent/*" element={<AgentApp />} />
        <Route path="/admin/*" element={<AdminApp />} />
        <Route path="*" element={<Navigate to="/" />} />
      </Routes></Suspense>
    </BrowserRouter>
  );
}
