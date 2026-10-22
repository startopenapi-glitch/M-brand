// One registry for navigation and capability identifiers; server still authenticates every endpoint.
export const adminResources=[
 {label:"工作台",path:"/admin",capability:"overview",group:"总览"},
 {label:"订单",path:"/admin/orders",capability:"orders",group:"交易与交付"},
 {label:"任务",path:"/admin/jobs",capability:"jobs",group:"交易与交付"},
 {label:"售后",path:"/admin/support",capability:"support",group:"交易与交付"},
 {label:"邀请码",path:"/admin/codes",capability:"codes",group:"增长与内容"},
 {label:"代理",path:"/admin/agents",capability:"agents",group:"增长与内容"},
 {label:"用量",path:"/admin/usage",capability:"usage",group:"系统"},
 {label:"审计",path:"/admin/audit",capability:"audit",group:"系统"}];
