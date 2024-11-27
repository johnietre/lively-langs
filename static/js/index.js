(function() {

function newLang() {
  return {
    id: 0,
    name: "",
    aliases: [],
    notes: "",
    words: [],
  }
}

const App = {
  data() {
    return {
      langs: [],
      currLang: newLang(),
      currLangIndex: -1,

      wordInput: "",
      words: [],

      __blankProperty: ""
    };
  },
  async mounted() {
    await this.loadLangs();
  },
  methods: {
    async loadLangs() {
      const url = new URL("langs", this.location.href);
      const resp = await fetch(url.toString());
      if (!resp.ok) {
        let errStr = await resp.text();
        try {
          const jsResp = JSON.parse(errStr);
          if (jsResp.error) {
            errStr = jsResp.error;
          }
        } catch {
        }
        console.log(`error loading languages: ${errStr}`);
        alert(`Error loading languages`);
        return;
      }
      try {
        this.langs = await resp.json();
      } catch (e) {
        console.log(`error parsing languages response JSON: ${e}`);
        alert(`Error loading languages`);
      }
    },
    changeCurrLang() {
      const currLangIndex = (typeof this.changeCurrLang === "number") ?
        this.changeCurrLang : parseInt(this.changeCurrLang);
      if (currLangIndex == -1) {
        this.currLang = newLang();
      } else {
        this.currLang = this.langs[this.currLangIndex];
      }
    },
    async getAllWords() {
      const url = new URL(`langs/${this.currLang.id}/words`, this.location.href);
      const resp = await fetch(url.toString());
      const jsStr = await resp.text();
      let jsResp;
      try {
        jsResp = JSON.parse(jsStr);
      } catch {
        console.log(`error parsing words response JSON: ${jsStr}`);
        alert(`Error loading all words`);
        return;
      }
      if (!resp.ok) {
        console.log(`error parsing words response JSON: ${errStr}`);
        alert(`Error loading all words`);
        return;
      }
      if (jsResp.content === undefined) {
        jsResp.content = [];
      }
      this.words = jsResp.content;
    },

    __blankMethod() {}
  }
};

const app = Vue.createApp(App);
app.mount("#app");

})()
