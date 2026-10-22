export type BrandCase = { id: number | string; title: string; tagline: string; images: { label: string; file: string; avif?: string }[] };
const base = "assets/examples/m-brand/";
export const brandPreviews: {file:string;avif?:string;label:string;description:string}[] = [
 {file:base+"hero.svg",label:"品牌主视觉",description:"从一个想法，到一个可以使用的品牌。"},
 {file:base+"system.svg",label:"品牌系统",description:"统一标志、色彩和文字，保持清晰表达。"},
 {file:base+"applications.svg",label:"应用落地",description:"把品牌设计用在名片、包装和日常沟通中。"},
];
export const brandExample: BrandCase = {id:"m-brand-own-example",title:"M-brand · 开源品牌设计示例",tagline:"开源仓库内置的原创矢量演示素材，不是客户作品。",images:[
 {file:base+"logo.svg",label:"Logo 标准组合"},{file:base+"system.svg",label:"品牌系统"},
 {file:base+"hero.svg",label:"品牌主视觉"},{file:base+"applications.svg",label:"应用落地"},
]};
const retiredExampleRoots = ["assets/shanwu/", "assets/examples/seedlab/", "gen/legacy-demo/"];
export function withBrandExample(cases: BrandCase[]): BrandCase[] {
 return [brandExample,...cases.filter(c=>c.images.length===0||!c.images.every(image=>retiredExampleRoots.some(root=>image.file.startsWith(root))))];
}
