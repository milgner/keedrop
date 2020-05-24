describe("noscript", function () {
  it("should show normally when script is enabled", function() {
    cy.visit("/");
    cy.contains("Cryptopgrahy is performed in the browser").should("not.visible");
    cy.contains("Please enable JavaScript").should("not.visible");
    cy.get("#encrypt").should("visible").should("be.enabled");
  });

  it("should show noscript banner when script is disabled", function() {
    cy.visit("/", { script: false });
    cy.contains("Cryptopgrahy is performed in the browser").should("exist");
    cy.get("button")
      .first()
      .contains("Please enable JavaScript")
      .should("be.disabled");
    cy.get("#encrypt").should("not.visible");
  });
});
