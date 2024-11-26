// Populate the text field values if a get request has already been made
var url = new URL(window.location.href)
document.getElementById("word").value = url.searchParams.get("word")
document.getElementById("word").disabled = (url.searchParams.get("all") != null)
document.getElementById("all").checked = (url.searchParams.get("all") != null)

// Used to check if the check box has been checked
function ischecked() {
  var all = document.getElementById("all");
  var field = document.getElementById("word");
  if (all.checked) { // Disable text field if the box is checked
    field.disabled = true;
  } else {
    field.disabled = false;
  }
}

// Used to put the page into "add mode"
function addWord() {
  // Change the text of <p id="msg"></p>
  document.getElementById("msg").innerHTML = "Add the word/phrase and its definition.";
  // Change the form method
  document.getElementById("form").method = "POST";
  // Hide the checkbox label and box
  document.getElementById("alllab").hidden = true;
  document.getElementById("all").hidden = true;
  // Reveal the definition label and text field as well as enable it
  document.getElementById("deflab").hidden = false;
  document.getElementById("def").hidden = false;
  document.getElementById("def").disabled = false;
  // Reveal the gender label and radio buttons
  document.getElementById("gen-m-lab").hidden = false;
  document.getElementById("gen-m").hidden = false;
  document.getElementById("gen-m").disabled = false;
  document.getElementById("gen-f-lab").hidden = false;
  document.getElementById("gen-f").hidden = false;
  document.getElementById("gen-f").disabled = false;
  document.getElementById("gen-b-lab").hidden = false;
  document.getElementById("gen-b").hidden = false;
  document.getElementById("gen-b").disabled = false;
  document.getElementById("gen-n-lab").hidden = false;
  document.getElementById("gen-n").hidden = false;
  document.getElementById("gen-n").disabled = false;
  document.getElementById("gen-i-lab").hidden = false;
  document.getElementById("gen-i").hidden = false;
  document.getElementById("gen-i").disabled = false;
  // Reveal the radios' line breaks
  document.querySelectorAll(".radio-br").forEach(br => {
    br.removeAttribute("hidden");
  });
  // Change the value of the "search" button to "Add"
  document.getElementById("search").value = "Add";
  // Reveal the "Cancel" button
  document.getElementById("cancel").hidden = false;
}

function addMode() {
  var inp = document.getElementById("addinp");
  var lab = document.getElementById("addinplab");
  var but = document.getElementById("addmbut");
  if (inp.hidden) {
    lab.hidden = false;
    inp.hidden = false;
    but.innerHTML = "Continue";
  } else {
    if (lab.value == "Rj385637") {
      input
      addWord();
    } else {
      inp.value = "Nope bitch";
    }
  }
}

// window.location.href = "http://localhost:8000?word="; // redirect that allows for back button
// window.location.replace("http://localhost:8000?word="); // redirect but no back button
