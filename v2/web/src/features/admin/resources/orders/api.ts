import {api} from "../../../../lib/api";
export type Order={id:string;amount_cents:number;channel:string;status:string;credited:boolean;mock:boolean;wx_transaction_id:string;code:string;visitor:string;created_at:string;paid_at:string;job_id:string};
export type OrderPage={items:Order[];total:number;page:number;page_size:number;summary:{revenue_cents:number;paid:number;pending:number}};
export const loadOrders=(params:URLSearchParams,signal:AbortSignal):Promise<OrderPage>=>api(`/api/admin/orders/page?${params}`,{signal});
// Quote every cell; neutralize spreadsheet formula prefixes in untrusted text.
export const csvCell=(value:unknown)=>{let s=String(value??"");if(/^[\s]*[=+@-]/.test(s))s="'"+s;return '"'+s.replaceAll('"','""')+'"'};
