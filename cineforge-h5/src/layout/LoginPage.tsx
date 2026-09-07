import React from "react";
import { ArrowRight, Eye, EyeOff, LoaderCircle } from "lucide-react";

const LOGIN_BACKGROUNDS = [
  "/login-backgrounds/login-bg-01.jpg",
  "/login-backgrounds/login-bg-02.jpg",
  "/login-backgrounds/login-bg-03.jpg",
  "/login-backgrounds/login-bg-04.jpg",
];

type LoginPageProps = {
  loading: boolean;
  error: string | null;
  onLogin: (phone: string, password: string) => void;
  onClearError: () => void;
};

export function LoginPage({ loading, error, onLogin, onClearError }: LoginPageProps) {
  const [phone, setPhone] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [showPassword, setShowPassword] = React.useState(false);
  const [backgroundIndex, setBackgroundIndex] = React.useState(0);
  const [loginOpen, setLoginOpen] = React.useState(false);
  const phoneValid = /^\d{11}$/.test(phone.trim());
  const passwordValid = password.length >= 6;

  React.useEffect(() => {
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    const timer = window.setInterval(() => {
      setBackgroundIndex((current) => (current + 1) % LOGIN_BACKGROUNDS.length);
    }, 3000);
    return () => window.clearInterval(timer);
  }, []);

  React.useEffect(() => {
    if (!loginOpen) return;
    function closeOnEscape(event: KeyboardEvent) {
      if (event.key === "Escape" && !loading) setLoginOpen(false);
    }
    window.addEventListener("keydown", closeOnEscape);
    return () => window.removeEventListener("keydown", closeOnEscape);
  }, [loginOpen, loading]);

  function submit(event: React.FormEvent) {
    event.preventDefault();
    if (!phoneValid || !passwordValid) return;
    onLogin(phone.trim(), password);
  }

  function updatePhone(value: string) {
    if (error) onClearError();
    setPhone(value.replace(/\D/g, ""));
  }

  function updatePassword(value: string) {
    if (error) onClearError();
    setPassword(value);
  }

  return (
    <main className="login-shell">
      <div className="login-backgrounds" aria-hidden="true">
        {LOGIN_BACKGROUNDS.map((src, index) => (
          <img
            className={`login-background${index === backgroundIndex ? " is-active" : ""}`}
            key={src}
            src={src}
            alt=""
          />
        ))}
      </div>
      <div className="login-page-brand" aria-label="CineForge">
        <img src="/cineforge-logo.png" alt="CineForge" />
      </div>
      <section className="login-promo" aria-label="CineForge 产品介绍">
        <img className="login-promo__logo" src="/cineforge-logo.png" alt="CineForge" />
        <p className="login-promo__features">导演审批、团队协作、剧本拆解</p>
        <button className="login-promo__cta" type="button" onClick={() => setLoginOpen(true)}>
          <span>去创作</span>
          <ArrowRight aria-hidden="true" size={18} strokeWidth={1.7} />
        </button>
      </section>
      {loginOpen ? (
        <div className="login-dialog" role="presentation" onMouseDown={(event) => {
          if (event.target === event.currentTarget && !loading) setLoginOpen(false);
        }}>
          <section aria-label="登录 CineForge" aria-modal="true" className="login-card login-card--focused" role="dialog">
            <form className="login-form" aria-busy={loading} onSubmit={submit}>
              <div className="login-form__heading">
                <h1>欢迎使用 CineForge</h1>
                <p>AI 漫剧生成工具台</p>
              </div>
              {error ? <div className="notice error login-form__error" role="alert">{error}</div> : null}
              <label className="login-field">
                <span>手机号码</span>
                <span className={`login-phone-input${phone.length > 0 && !phoneValid ? " is-error" : ""}`}>
                  <b>+86</b>
                  <input
                    aria-describedby={phone.length > 0 && !phoneValid ? "login-phone-error" : undefined}
                    aria-invalid={phone.length > 0 && !phoneValid}
                    autoComplete="tel"
                    autoFocus
                    className="login-control"
                    inputMode="numeric"
                    maxLength={11}
                    placeholder="请输入手机号码"
                    required
                    value={phone}
                    onChange={(event) => updatePhone(event.target.value)}
                  />
                </span>
                {phone.length > 0 && !phoneValid ? <em id="login-phone-error">请输入 11 位手机号码</em> : null}
              </label>
              <label className="login-field">
                <span>密码</span>
                <span className="login-password-input">
                  <input
                    autoComplete="current-password"
                    aria-describedby={password.length > 0 && !passwordValid ? "login-password-error" : undefined}
                    aria-invalid={password.length > 0 && !passwordValid}
                    className="login-control"
                    minLength={6}
                    placeholder="请输入至少 6 位密码"
                    required
                    type={showPassword ? "text" : "password"}
                    value={password}
                    onChange={(event) => updatePassword(event.target.value)}
                  />
                  <button type="button" aria-label={showPassword ? "隐藏密码" : "显示密码"} onClick={() => setShowPassword((value) => !value)}>
                    {showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
                  </button>
                </span>
                {password.length > 0 && !passwordValid ? <em id="login-password-error">密码至少需要 6 位</em> : null}
              </label>
              <button className="login-submit login-submit--white" disabled={loading || !phoneValid || !passwordValid} type="submit">
                {loading ? <><LoaderCircle className="login-submit__spinner" size={17} />登录中</> : "登录"}
              </button>
            </form>
          </section>
        </div>
      ) : null}
    </main>
  );
}