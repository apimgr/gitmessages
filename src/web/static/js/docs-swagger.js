(()=>{
  const mount=document.getElementById('swagger-ui');
  if(!mount||!window.SwaggerUIBundle||!window.SwaggerUIStandalonePreset)return;
  window.SwaggerUIBundle({
    url:mount.dataset.specUrl,
    dom_id:'#swagger-ui',
    deepLinking:true,
    displayRequestDuration:true,
    defaultModelsExpandDepth:-1,
    persistAuthorization:false,
    presets:[window.SwaggerUIBundle.presets.apis,window.SwaggerUIStandalonePreset],
    layout:'StandaloneLayout',
    validatorUrl:null
  });
})();
