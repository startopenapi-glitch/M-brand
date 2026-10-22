import { useEffect, useState } from "react";
import { Link, NavLink, Outlet, useLocation } from "react-router-dom";
import { FolderOpen, Menu, X } from "lucide-react";
import * as Dialog from "@radix-ui/react-dialog";

export function BrandMark() {
  return <Link to="/" className="brand-mark" aria-label="M-brand 首页"><img src="/assets/brand/symbol.svg" width={25} height={38} alt="" /><span>M-brand</span></Link>;
}
export default function SiteLayout() {
  const { pathname, search } = useLocation();
  const [menu, setMenu] = useState(false);
  useEffect(() => { setMenu(false); window.scrollTo(0, 0); }, [pathname]);
  useEffect(() => {
    const ref = new URLSearchParams(search).get("ref");
    if (ref && /^[A-Za-z0-9]{4,16}$/.test(ref)) document.cookie = `zb_ref=${ref}; path=/; max-age=2592000; SameSite=Lax${location.protocol === "https:" ? "; Secure" : ""}`;
  }, [search]);
  const creating = pathname === "/" || pathname === "/create";
  return <div className="site">
    <a href="#page-content" className="skip-link">跳到主要内容</a>
    <header className="site-header"><div className="site-header-inner">
      <BrandMark />
      <nav className="site-tabs" aria-label="主要导航"><Link to="/" className={creating ? "active" : ""} aria-current={creating ? "page" : undefined}>创建品牌</Link><NavLink to="/gallery">作品案例</NavLink></nav>
      <div className="header-actions">
        <NavLink to="/orders" className="my-proposals"><FolderOpen size={17} aria-hidden="true" /><span>我的提案</span></NavLink>
        <Dialog.Root open={menu} onOpenChange={setMenu}>
          <Dialog.Trigger className="icon-button mobile-menu" aria-label="打开导航"><Menu size={21} /></Dialog.Trigger>
          <Dialog.Portal><Dialog.Overlay className="dialog-overlay" /><Dialog.Content className="dialog-card navigation-dialog" aria-describedby={undefined}>
            <Dialog.Title>导航</Dialog.Title><Dialog.Close className="dialog-close icon-button" aria-label="关闭导航"><X size={20} /></Dialog.Close>
            <nav aria-label="手机导航"><Link to="/" onClick={()=>setMenu(false)}>创建品牌</Link><Link to="/gallery" onClick={()=>setMenu(false)}>作品案例</Link><Link to="/pricing" onClick={()=>setMenu(false)}>费用说明</Link></nav>
          </Dialog.Content></Dialog.Portal>
        </Dialog.Root>
      </div>
    </div></header>
    <div id="page-content" tabIndex={-1} className="site-content"><Outlet /></div>
    <footer className="site-footer"><div><span>© {new Date().getFullYear()} M-brand · 你的品牌，从0开始。</span><p>联系支持：请联系站点管理员</p></div><nav aria-label="页脚导航"><Link to="/pricing">费用说明</Link><Link to="/redeem">兑换已购邀请码</Link><Link to="/agent">代理工作台</Link><a href="/docs/user-agreement" target="_blank" rel="noreferrer">服务条款</a><a href="/docs/refund" target="_blank" rel="noreferrer">退款政策</a></nav></footer>
  </div>;
}
