(()=>{
  const mount=document.getElementById('graphiql');
  if(!mount||!window.React||!window.ReactDOM||!window.GraphiQL)return;
  const fetcher=(params,options)=>fetch(mount.dataset.endpoint,{
    method:'POST',
    headers:Object.assign({'Accept':'application/json','Content-Type':'application/json'},options&&options.headers),
    credentials:'same-origin',
    body:JSON.stringify(params)
  }).then(response=>response.json());
  window.ReactDOM.render(window.React.createElement(window.GraphiQL,{
    fetcher,
    query:'{ types { type description emoji example breaking } }'
  }),mount);
})();
