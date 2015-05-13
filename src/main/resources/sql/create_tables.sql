CREATE CACHED TABLE tbl_company(
  id INTEGER IDENTITY,
  company OBJECT
  ) ;

CREATE CACHED TABLE tbl_inpayment(
  id Integer IDENTITY,
  number INTEGER,
  inpayment OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id)
  ) ;

CREATE CACHED TABLE tbl_invoice(
  id INTEGER IDENTITY,
  number INTEGER,
  invoice OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id)
  ) ;

CREATE CACHED TABLE tbl_creditinvoice(
  id INTEGER IDENTITY,
  number INTEGER,
  creditinvoice OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id)
  ) ;

CREATE CACHED TABLE tbl_periodicinvoice(
  id INTEGER IDENTITY,
  number INTEGER,
  periodicinvoice OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id)
  ) ;

CREATE CACHED TABLE tbl_outpayment(
  id INTEGER IDENTITY,
  number INTEGER,
  outpayment OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id)
  ) ;

CREATE CACHED TABLE tbl_supplierinvoice(
  id INTEGER IDENTITY,
  number INTEGER,
  supplierinvoice OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id)
  ) ;

CREATE CACHED TABLE tbl_purchaseorder(
  id INTEGER IDENTITY,
  number INTEGER,
  purchaseorder OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id)
  ) ;

CREATE CACHED TABLE tbl_order(
  id INTEGER IDENTITY,
  number INTEGER,
  iorder OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id)
  ) ;

CREATE CACHED TABLE tbl_tender(
  id INTEGER IDENTITY,
  number INTEGER,
  tender OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id)
  ) ;

CREATE CACHED TABLE tbl_suppliercreditinvoice(
  id INTEGER IDENTITY,
  number INTEGER,
  suppliercreditinvoice OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id)
  ) ;

CREATE CACHED TABLE tbl_vouchertemplate(
  name VARCHAR,
  vouchertemplate OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id),
  PRIMARY KEY(name,companyid)
  ) ;

CREATE CACHED TABLE tbl_project(
  number VARCHAR,
  project OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id),
  PRIMARY KEY(number,companyid)
  ) ;

CREATE CACHED TABLE tbl_resultunit(
  number VARCHAR,
  resultunit OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id),
  PRIMARY KEY(number,companyid)
  ) 

CREATE CACHED TABLE tbl_product(
  id INTEGER IDENTITY,
  number VARCHAR,
  product OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id)
  ) ;

CREATE CACHED TABLE tbl_customer(
  id INTEGER IDENTITY,
  number VARCHAR,
  customer OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id)
  ) ;

CREATE CACHED TABLE tbl_supplier(
  id INTEGER IDENTITY,
  number VARCHAR,
  supplier OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id)
  ) ;

CREATE CACHED TABLE tbl_autodist(
  id INTEGER IDENTITY,
  number INTEGER,
  autodist OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id)
  ) ;

CREATE CACHED TABLE tbl_indelivery(
  id INTEGER IDENTITY,
  number INTEGER,
  indelivery OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id)
  ) ;

CREATE CACHED TABLE tbl_outdelivery(
  id INTEGER IDENTITY,
  number INTEGER,
  outdelivery OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id)
  ) ;

CREATE CACHED TABLE tbl_inventory(
  id INTEGER IDENTITY,
  number INTEGER,
  inventory OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id)
  ) ;

CREATE CACHED TABLE tbl_currency(
  code VARCHAR PRIMARY KEY,
  currency OBJECT
  ) ;

CREATE CACHED TABLE tbl_unit(
  name VARCHAR PRIMARY KEY,
  unit OBJECT
  ) ;

CREATE CACHED TABLE tbl_deliveryway(
  name VARCHAR PRIMARY KEY,
  deliveryway OBJECT
  ) ;

CREATE CACHED TABLE tbl_deliveryterm(
  name VARCHAR PRIMARY KEY,
  deliveryterm OBJECT
  ) ;

CREATE CACHED TABLE tbl_paymentterm(
  name VARCHAR PRIMARY KEY,
  paymentterm OBJECT
  ) ;

CREATE CACHED TABLE tbl_accountplan(
  id INTEGER IDENTITY,
  accountplan OBJECT
  ) ;

CREATE CACHED TABLE tbl_accountingyear(
  id INTEGER IDENTITY,
  accountingyear OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id)
  ) ;

CREATE CACHED TABLE tbl_voucher(
  id INTEGER IDENTITY,
  number INTEGER,
  voucher OBJECT,
  yearid INTEGER,
  FOREIGN KEY(yearid) REFERENCES tbl_accountingyear(id)
  ) ;

CREATE CACHED TABLE tbl_license(
  licensekey VARCHAR PRIMARY KEY
  );

CREATE CACHED TABLE tbl_ownreport(
  id INTEGER IDENTITY,
  ownreport OBJECT,
  companyid INTEGER,
  FOREIGN KEY(companyid) REFERENCES tbl_company(id)
  ) ;
