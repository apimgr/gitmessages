(()=>{
'use strict';
const root=document.documentElement;
const stamp=root.dataset.build||'';
const base=root.dataset.basePath||'';
const appURL=value=>base+value;
const consentCookie=()=>document.cookie.split('; ').find(item=>item.startsWith('cookie_consent='));
const preferenceConsent=()=>{
  const cookie=consentCookie();
  if(!cookie)return false;
  try{return JSON.parse(decodeURIComponent(cookie.slice(cookie.indexOf('=')+1))).preferences===true}catch{return false}
};
const previewTheme=value=>{
  if(!['dark','light','auto'].includes(value))return;
  const resolved=value==='auto'?(matchMedia('(prefers-color-scheme: dark)').matches?'dark':'light'):value;
  root.dataset.theme=resolved;
  root.classList.remove('theme-dark','theme-light');
  root.classList.add(`theme-${resolved}`);
};
if(preferenceConsent()){
  const match=document.cookie.match(/(?:^|; )theme=([^;]+)/);
  if(match)previewTheme(decodeURIComponent(match[1]));
}
document.addEventListener('change',event=>{
  if(event.target.name==='theme')previewTheme(event.target.value);
});
document.addEventListener('click',async event=>{
  const preferences=event.target.closest('[data-action="cookie-preferences"]');
  if(preferences){
    const dialog=document.getElementById('cookie-preferences-dialog');
    if(dialog?.showModal)dialog.showModal();
    else window.location.href='/server/preferences#privacy-preferences';
  }
  if(event.target.closest('[data-action="cookie-preferences-close"]'))document.getElementById('cookie-preferences-dialog')?.close();
  const button=event.target.closest('[data-copy-text],[data-copy-target]');
  if(!button)return;
  const target=button.dataset.copyTarget?document.getElementById(button.dataset.copyTarget):null;
  const text=button.dataset.copyText??target?.textContent?.trim()??'';
  if(!text||!navigator.clipboard?.writeText)return;
  try{
    await navigator.clipboard.writeText(text);
    const icon=button.querySelector('.copy-icon');
    const label=button.querySelector('.copy-text');
    if(icon)icon.textContent='✓';
    if(label)label.textContent=button.dataset.copiedLabel||'Copied!';
    if(!icon&&!label)button.textContent='✓ '+(button.dataset.copiedLabel||'Copied!');
    button.classList.add('copied');
    setTimeout(()=>{
      if(icon)icon.textContent=button.dataset.copyIcon||'📋';
      if(label)label.textContent=button.dataset.copyLabel||'Copy';
      if(!icon&&!label)button.textContent=button.dataset.copyLabel||'Copy';
      button.classList.remove('copied');
    },2000);
  }catch{}
});
fetch(appURL('/locales/'+encodeURIComponent(root.lang)+'.json')).then(response=>response.ok?response.json():Promise.reject()).then(locale=>{window.GitMessagesLocale=locale}).catch(()=>{});
const offline=document.getElementById('offline-indicator');
const updatePrompt=document.getElementById('pwa-update-prompt');
const updateButton=document.getElementById('pwa-update-now');
const installSection=document.getElementById('pwa-install');
const installButton=document.getElementById('pwa-install-button');
const iosInstall=document.getElementById('pwa-ios-install');
const dirtyForms=new WeakSet();
document.querySelectorAll('form').forEach(form=>{
  const markDirty=()=>dirtyForms.add(form);
  form.addEventListener('input',markDirty);
  form.addEventListener('change',markDirty);
});
const hasDirtyForm=()=>[...document.forms].some(form=>dirtyForms.has(form));
const reloadOnce=nextStamp=>{
  if(sessionStorage.getItem('gitmessages_recovered')===nextStamp)return;
  sessionStorage.setItem('gitmessages_recovered',nextStamp);
  window.location.reload();
};
let deferredInstallPrompt;
const standalone=window.matchMedia('(display-mode: standalone)').matches||window.navigator.standalone===true;
if(installSection&&!standalone){
  window.addEventListener('beforeinstallprompt',event=>{event.preventDefault();deferredInstallPrompt=event;installSection.hidden=false;if(installButton)installButton.hidden=false;});
  if(/iphone|ipad|ipod/i.test(navigator.userAgent)&&/safari/i.test(navigator.userAgent)&&!/(crios|fxios|edgios)/i.test(navigator.userAgent)&&iosInstall){installSection.hidden=false;iosInstall.hidden=false;if(installButton)installButton.hidden=true;}
  if(installButton)installButton.addEventListener('click',async()=>{if(!deferredInstallPrompt)return;deferredInstallPrompt.prompt();await deferredInstallPrompt.userChoice;deferredInstallPrompt=null;installSection.hidden=true;});
  window.addEventListener('appinstalled',()=>{installSection.hidden=true;deferredInstallPrompt=null;});
}
const updateState=()=>{if(offline)offline.hidden=navigator.onLine;};
updateState();window.addEventListener('online',updateState);window.addEventListener('offline',updateState);
const showUpdate=()=>{if(updatePrompt)updatePrompt.hidden=false;};
const originalFetch=window.fetch.bind(window);
window.fetch=async(...args)=>{const response=await originalFetch(...args);const nextStamp=response.headers.get('X-Build');if(nextStamp&&nextStamp!==stamp){if(hasDirtyForm())showUpdate();else window.setTimeout(()=>reloadOnce(nextStamp),0);}return response;};
let waitingWorker=null;let acceptedUpdate=false;
if(updateButton)updateButton.addEventListener('click',()=>{if(waitingWorker){acceptedUpdate=true;waitingWorker.postMessage({type:'SKIP_WAITING'});window.setTimeout(()=>{if(acceptedUpdate){acceptedUpdate=false;reloadOnce(stamp);}},5000);return;}reloadOnce(stamp);});
if('serviceWorker'in navigator){
  navigator.serviceWorker.addEventListener('controllerchange',()=>{if(!acceptedUpdate)return;acceptedUpdate=false;reloadOnce(stamp);});
  window.addEventListener('load',()=>navigator.serviceWorker.register(appURL('/sw.js'),{scope:appURL('/')}).then(registration=>{if(registration.waiting&&navigator.serviceWorker.controller){waitingWorker=registration.waiting;showUpdate();}registration.addEventListener('updatefound',()=>{const worker=registration.installing;if(!worker)return;worker.addEventListener('statechange',()=>{if(worker.state==='installed'&&navigator.serviceWorker.controller){waitingWorker=registration.waiting||worker;showUpdate();}});});}).catch(()=>{}));
}
})();
