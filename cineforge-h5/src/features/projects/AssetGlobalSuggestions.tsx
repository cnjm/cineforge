import React from "react";
import { X } from "lucide-react";
import { aggregateGlobalReviewSuggestions } from "../../assetReviewDisplay";
import type { AssetNormalizationGlobalReviewItem } from "../../types";

export function AssetGlobalSuggestions({ items }: { items: AssetNormalizationGlobalReviewItem[] }) {
  const [open, setOpen] = React.useState(false);
  const suggestions = React.useMemo(() => aggregateGlobalReviewSuggestions(items), [items]);
  if (!suggestions.length) return null;
  return <>
    <button className="asset-global-suggestion-link" type="button" onClick={() => setOpen(true)}>Agent 建议（{suggestions.length}）</button>
    {open ? <div className="modal-backdrop" role="presentation" onClick={() => setOpen(false)}><section className="asset-scheme-modal asset-completion-modal asset-global-suggestions-modal" role="dialog" aria-modal="true" aria-label="Agent 建议" onClick={(event) => event.stopPropagation()}>
      <div className="modal-head"><div><h3>Agent 建议</h3><p>仅供人工调整时参考，不影响资产确认。</p></div><button className="btn" type="button" onClick={() => setOpen(false)}><X aria-hidden="true" size={16} />关闭</button></div>
      <div className="asset-global-suggestion-list">{suggestions.map((suggestion, index) => <p key={`${suggestion}-${index}`}>{suggestion}</p>)}</div>
    </section></div> : null}
  </>;
}
