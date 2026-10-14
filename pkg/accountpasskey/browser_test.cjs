const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname,'../../web/static/account-passkeys.js'),'utf8');

function loginBrowser(supported, hostname = 'site.example') {
    const redirects = [], requests = [];
    const status = {textContent:''}, panel = {hidden:false};
    const attributes = {'data-passkey-start':'?login&passkey_begin','data-passkey-finish':'?login&passkey_finish','data-passkey-status':'#status','data-passkey-error':'Try the passkey again'};
    const loginButton = {listeners:{},getAttribute:name=>attributes[name],addEventListener(name,callback){this.listeners[name]=callback;}};
    const fallback = {href:'https://site.example/?login&password_login=1&return_path=%2Fdocs'};
    const credential = {id:'key',rawId:new Uint8Array([0]).buffer,type:'public-key',getClientExtensionResults:()=>({}),response:{clientDataJSON:new Uint8Array([0]).buffer,authenticatorData:new Uint8Array([0]).buffer,signature:new Uint8Array([0]).buffer}};
    const credentials = {
        create:async()=>credential,
        get:async options=>{requests.push(options);return credential;}
    };
    const browserWindow = {
        isSecureContext:true,PublicKeyCredential:supported?function PublicKeyCredential(){}:undefined,
        location:{hostname,replace:target=>redirects.push(target),assign:target=>redirects.push(target)},
        atob:encoded=>Buffer.from(encoded,'base64').toString('binary'),btoa:binary=>Buffer.from(binary,'binary').toString('base64')
    };
    const elements = {'[data-passkey-login]':loginButton,'[data-password-fallback]':fallback,'[data-passkey-login-panel]':panel,'#status':status};
    vm.runInNewContext(source,{
        window:browserWindow,navigator:{credentials},document:{querySelector:selector=>elements[selector]||null},
        fetch:async(address,options)=>({ok:true,json:async()=>options.method==='POST'?{redirect:'/docs'}:{token:'challenge',options:{publicKey:{challenge:'AA',allowCredentials:[]}}}})
    });
    return {redirects,requests,credentials,loginButton,status,panel};
}

(async function verifyPasskeyPriorityAndFallback() {
    for (const unsupported of [loginBrowser(false),loginBrowser(true,'192.0.2.1')]) {
        assert.equal(unsupported.panel.hidden,true);
        assert.match(unsupported.redirects[0],/password_login=1&return_path=%2Fdocs/);
    }
    const supported = loginBrowser(true);
    assert.equal(supported.redirects.length,0,'supported browsers must offer the key first');
    await supported.loginButton.listeners.click();
    assert.ok(supported.requests[0].publicKey);
    assert.equal(supported.requests[0].password,undefined,'do not request password autofill during passkey sign-in');
    assert.equal(supported.redirects[0],'/docs');
    const cancelled = loginBrowser(true);
    cancelled.credentials.get=async()=>{throw Object.assign(new Error('cancelled'),{name:'NotAllowedError'});};
    await cancelled.loginButton.listeners.click();
    assert.equal(cancelled.loginButton.disabled,false);
    assert.equal(cancelled.status.textContent,'Try the passkey again');
    assert.equal(cancelled.redirects.length,0,'cancelling the key leaves the explicit password fallback available');
    console.log('Passkeys: primary credential request, unsupported-device fallback, cancellation and return path passed');
})().catch(error=>{console.error(error);process.exitCode=1;});
