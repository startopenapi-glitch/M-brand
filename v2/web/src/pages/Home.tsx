import { useState } from "react";
import { Link } from "react-router-dom";
import { ArrowRight } from "lucide-react";
import BrandBriefForm from "../components/BrandBriefForm";
import { brandPreviews as previews } from "../lib/brandExamples";
export default function Home() {
  const [preview, setPreview] = useState(0);
  return <main className="home-page">
    <section className="creation-layout" aria-label="创建品牌提案">
      <div className="brand-showcase">
        <div className="showcase-heading"><span className="eyebrow">从一个想法，到一个品牌</span><h2>你的品牌，<br />从0开始。</h2><p>先看方向，再把设计用起来。</p></div>
        <div className="showcase-art"><img key={preview} src={`/${previews[preview].file}`} alt={`M-brand 演示 · ${previews[preview].label}`} width={1200} height={800} fetchPriority={preview === 0 ? "high" : "auto"} /></div>
        <div className="showcase-caption"><div><strong>M-brand</strong><span>自有品牌设计示例</span></div><p>{previews[preview].description}</p></div>
        <div className="preview-switch" aria-label="成果示例">{previews.map((item,i) => <button type="button" key={item.file} aria-pressed={i === preview} onClick={() => setPreview(i)}>{item.label}</button>)}</div>
      </div>
      <section className="surface creation-card"><div className="card-heading"><span className="eyebrow">为你的品牌，找到一个好开始</span><h1>创建品牌提案</h1><p>告诉我们品牌名，先看 3 个 Logo 方向。</p></div><BrandBriefForm /></section>
    </section>
    <section className="journey-strip" aria-label="使用步骤">{[["01","看方向","填写品牌信息，9.9 元解锁自己的品牌。"],["02","选交付","选定方向后，挑选你需要的品牌素材。"],["03","下载成果","付款后生成已购内容，随时回来查看。"]].map(([n,title,description]) => <div key={n}><span className="step-index">{n}</span><div><h2>{title}</h2><p>{description}</p></div></div>)}</section>
    <div className="home-more"><p>想先了解交付内容？</p><Link to="/gallery">看看品牌案例 <ArrowRight size={15} aria-hidden="true" /></Link><Link to="/pricing">查看费用说明</Link></div>
  </main>;
}
